package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
)

const (
	vnicKind = "VNIC"
	// A pending VNIC has no private IP yet and cannot be attached.
	statusVNICPending = "pending"
	// An attached VNIC has a place in its machine's ordered VNIC list.
	statusVNICAttached = "attached"
)

// VNIC is a virtual network interface in a subnet. The API addresses it by the UUID
// that ends its URN.
type VNIC struct {
	ID        string
	URN       string
	Name      string
	SubnetURN string
	VMURN     string
	PrivateIP string
	PublicIP  string
	MAC       string
	Status    string
	LastError string
	Tags      []Tag
}

// CreateVNICRequest describes a VNIC to create with POST /vnics.
type CreateVNICRequest struct {
	Name      string
	SubnetURN string
	Tags      []Tag
}

func (v VNIC) lifecycle() (string, string) { return v.Status, v.LastError }

func (v VNIC) tags() []Tag { return v.Tags }

// VNICIDFromURN returns the UUID that ends a VNIC URN, which the API uses as its path
// identifier.
func VNICIDFromURN(urn string) string {
	return urn[strings.LastIndex(urn, ":")+1:]
}

// CreateVNIC waits until the VNIC has left pending, so it has its private IP and can be
// attached to a virtual machine.
func (c *Runtime) CreateVNIC(ctx context.Context, request CreateVNICRequest) (VNIC, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	body := infra.CreateVNICJSONRequestBody{
		Name:      optionalString(request.Name),
		SubnetURN: request.SubnetURN,
		Tags:      infraTags(request.Tags),
	}
	var wire infra.VNICResponse
	err := callInfra(operationCtx, func(ctx context.Context) (*http.Response, error) {
		return c.infraAPI().CreateVNIC(ctx, &infra.CreateVNICParams{XTenant: c.tenant()}, body, c.infraEditor())
	}, &wire)
	if err != nil {
		return VNIC{}, err
	}
	created := vnicFromInfra(wire)
	if created.ID == "" {
		return VNIC{}, fmt.Errorf("create %s in subnet %q: response carries no urn", vnicKind, request.SubnetURN)
	}
	return waitUntil(operationCtx, c, vnicKind, created.ID, c.vnicReader(created.ID), func(current VNIC) bool {
		return !strings.EqualFold(current.Status, statusVNICPending)
	}, "finish provisioning")
}

// GetVNIC reads one VNIC by its UUID.
func (c *Runtime) GetVNIC(ctx context.Context, id string) (VNIC, error) {
	var wire infra.VNICResponse
	err := callInfra(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(id); err != nil {
			return nil, err
		}
		return c.infraAPI().GetVNIC(ctx, id, &infra.GetVNICParams{XTenant: c.tenant()}, c.infraEditor())
	}, &wire)
	if err != nil {
		return VNIC{}, err
	}
	return vnicFromInfra(wire), nil
}

// RenameVNIC sets a VNIC's name and waits until a read reports it. The name changes
// through PUT /vnics/{vnicID}, which also sets the public IP and clears it when left out,
// so the current public IP is sent along unchanged.
func (c *Runtime) RenameVNIC(ctx context.Context, id, name string) (VNIC, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	current, err := c.GetVNIC(operationCtx, id)
	if err != nil {
		return VNIC{}, fmt.Errorf("rename %s %q: %w", vnicKind, id, err)
	}
	body := infra.UpdateVNICJSONRequestBody{Name: &name, PublicIP: optionalString(current.PublicIP)}
	err = callInfra[infra.VNICResponse](operationCtx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(id); err != nil {
			return nil, err
		}
		return c.infraAPI().UpdateVNIC(ctx, id, &infra.UpdateVNICParams{XTenant: c.tenant()}, body, c.infraEditor())
	}, nil)
	if err != nil {
		return VNIC{}, fmt.Errorf("rename %s %q: %w", vnicKind, id, err)
	}
	return waitUntil(operationCtx, c, vnicKind, id, c.vnicReader(id), func(current VNIC) bool {
		return current.Name == name
	}, "report its new name")
}

// UpdateVNICTags patches tags and reads back the resulting set.
func (c *Runtime) UpdateVNICTags(ctx context.Context, id string, current, desired []Tag) (VNIC, error) {
	patch := func(ctx context.Context, body infra.PatchTagsRequest) error {
		return c.patchVNIC(ctx, id, infra.PatchVNICJSONRequestBody(body))
	}
	return reconcileTags(ctx, c, vnicKind, id, patch, current, desired, c.vnicReader(id))
}

func (c *Runtime) patchVNIC(ctx context.Context, id string, body infra.PatchVNICJSONRequestBody) error {
	return callInfra[infra.VNICResponse](ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(id); err != nil {
			return nil, err
		}
		return c.infraAPI().PatchVNIC(ctx, id, &infra.PatchVNICParams{XTenant: c.tenant()}, body, c.infraEditor())
	}, nil)
}

// DeleteVNIC waits for deletion. The API refuses to delete an attached VNIC with a
// conflict; it is retried because a virtual machine destroyed in the same apply releases
// its VNICs asynchronously.
func (c *Runtime) DeleteVNIC(ctx context.Context, id string) error {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	remove := func(ctx context.Context) error {
		return callInfra[infra.DeleteVNICResponse](ctx, func(ctx context.Context) (*http.Response, error) {
			if err := checkSegments(id); err != nil {
				return nil, err
			}
			return c.infraAPI().DeleteVNIC(ctx, id, &infra.DeleteVNICParams{XTenant: c.tenant()}, c.infraEditor())
		}, nil)
	}
	for {
		err := deleteAndWait(operationCtx, c, vnicKind, id, remove, c.vnicReader(id))
		if !errors.Is(err, ErrResourceConflict) {
			return err
		}
		if waitErr := c.waitPoll(operationCtx); waitErr != nil {
			return fmt.Errorf("delete %s %q: still attached to a virtual machine: %w", vnicKind, id, err)
		}
	}
}

func (c *Runtime) vnicReader(id string) readFunc[VNIC] {
	return func(ctx context.Context) (VNIC, error) { return c.GetVNIC(ctx, id) }
}

func vnicFromInfra(wire infra.VNICResponse) VNIC {
	return VNIC{
		ID:        VNICIDFromURN(value(wire.Urn)),
		URN:       value(wire.Urn),
		Name:      value(wire.Name),
		SubnetURN: value(wire.SubnetURN),
		VMURN:     value(wire.VMURN),
		PrivateIP: value(wire.PrivateIP),
		PublicIP:  value(wire.PublicIP),
		MAC:       value(wire.Mac),
		Status:    enumString(wire.Status),
		LastError: value(wire.ErrorReason),
		Tags:      tagsFromInfra(wire.Tags),
	}
}
