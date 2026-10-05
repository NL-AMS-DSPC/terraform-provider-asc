package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
)

const (
	vpcKind    = "VPC"
	subnetKind = "subnet"
	vmKind     = "VM"
)

func (c *Runtime) CreateVPC(ctx context.Context, request CreateVPCRequest) (VPC, error) {
	body := infra.CreateVPCJSONRequestBody{Name: request.Name, Tags: infraTags(request.Tags)}
	if request.Subnets != nil {
		body.Subnets = make([]infra.Subnet, len(request.Subnets))
		for i, subnet := range request.Subnets {
			body.Subnets[i] = infra.Subnet{
				Name:  subnet.Name,
				Cidr:  subnet.CIDR,
				VpcID: optionalString(subnet.VPCID),
				Type:  infra.SubnetType(subnet.Type),
				Tags:  infraTags(subnet.Tags),
			}
		}
	}
	return createWithNameReservation(ctx, c, vpcKind, request.Name,
		func(ctx context.Context) error {
			return callInfra[infra.CreateVPCResponse](ctx, func(ctx context.Context) (*http.Response, error) {
				return c.infraAPI().CreateVPC(ctx, &infra.CreateVPCParams{XTenant: c.tenant()}, body, c.infraEditor())
			}, nil)
		},
		c.vpcReader(request.Name),
	)
}

func (c *Runtime) GetVPC(ctx context.Context, name string) (VPC, error) {
	var wire infra.VPCResponse
	err := callInfra(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(name); err != nil {
			return nil, err
		}
		return c.infraAPI().GetVPC(ctx, name, &infra.GetVPCParams{XTenant: c.tenant()}, c.infraEditor())
	}, &wire)
	if err != nil {
		return VPC{}, err
	}
	return vpcFromInfra(wire), nil
}

// UpdateVPCTags patches tags and reads back the resulting set.
func (c *Runtime) UpdateVPCTags(ctx context.Context, name string, current, desired []Tag) (VPC, error) {
	patch := func(ctx context.Context, body infra.PatchTagsRequest) error {
		return callInfra[infra.VPCResponse](ctx, func(ctx context.Context) (*http.Response, error) {
			if err := checkSegments(name); err != nil {
				return nil, err
			}
			return c.infraAPI().PatchVPC(ctx, name, &infra.PatchVPCParams{XTenant: c.tenant()}, body, c.infraEditor())
		}, nil)
	}
	return reconcileTags(ctx, c, vpcKind, name, patch, current, desired, c.vpcReader(name))
}

func (c *Runtime) DeleteVPC(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, vpcKind, name,
		func(ctx context.Context) error {
			return callInfra[infra.DeleteVPCResponse](ctx, func(ctx context.Context) (*http.Response, error) {
				if err := checkSegments(name); err != nil {
					return nil, err
				}
				return c.infraAPI().DeleteVPC(ctx, name, &infra.DeleteVPCParams{XTenant: c.tenant()}, c.infraEditor())
			}, nil)
		},
		c.vpcReader(name),
	)
}

func (c *Runtime) CreateSubnet(ctx context.Context, vpcName string, request CreateSubnetRequest) (Subnet, error) {
	body, err := createSubnetBody(request)
	if err != nil {
		return Subnet{}, err
	}
	err = callInfra[infra.SubnetResponse](ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(vpcName); err != nil {
			return nil, err
		}
		return c.infraAPI().CreateSubnetWithBody(ctx, vpcName, &infra.CreateSubnetParams{XTenant: c.tenant()},
			"application/json", bytes.NewReader(body), c.infraEditor())
	}, nil)
	if err != nil {
		return Subnet{}, err
	}
	return c.GetSubnet(ctx, vpcName, request.Name)
}

// createSubnetBody preserves the provider's omission of an unset vpcID. The outer
// field overrides the generated field's JSON tag, so the request needs only one
// encoding pass. Resource creation supplies the parent VPC's ID.
func createSubnetBody(request CreateSubnetRequest) ([]byte, error) {
	return json.Marshal(struct {
		infra.CreateSubnetJSONRequestBody
		VPCID *string `json:"vpcID,omitempty"`
	}{
		CreateSubnetJSONRequestBody: infra.CreateSubnetJSONRequestBody{
			Name: request.Name,
			Cidr: request.CIDR,
			Type: infra.SubnetType(request.Type),
			Tags: infraTags(request.Tags),
		},
		VPCID: optionalString(request.VPCID),
	})
}

// ListSubnets decodes the gateway's single envelope around the array; the generated
// response annotation describes an array of envelopes instead.
func (c *Runtime) ListSubnets(ctx context.Context, vpcName string) ([]Subnet, error) {
	var wire []infra.SubnetResponse
	err := callInfra(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(vpcName); err != nil {
			return nil, err
		}
		return c.infraAPI().ListSubnets(ctx, vpcName, &infra.ListSubnetsParams{XTenant: c.tenant()}, c.infraEditor())
	}, &wire)
	if err != nil {
		return nil, err
	}
	return subnetsFromInfra(&wire), nil
}

// GetSubnet reads one subnet by name. infra-gateway serves no single-subnet
// route, so the VPC's subnets are listed and matched on the exact name.
func (c *Runtime) GetSubnet(ctx context.Context, vpcName, subnetName string) (Subnet, error) {
	subnets, err := c.ListSubnets(ctx, vpcName)
	if err != nil {
		return Subnet{}, err
	}
	for _, subnet := range subnets {
		if subnet.Name == subnetName {
			return subnet, nil
		}
	}
	return Subnet{}, fmt.Errorf("%w: subnet %q in VPC %q", ErrResourceNotFound, subnetName, vpcName)
}

// UpdateSubnetTags moves a subnet's tags from current to desired.
func (c *Runtime) UpdateSubnetTags(ctx context.Context, vpcName, subnetName string, current, desired []Tag) (Subnet, error) {
	patch := func(ctx context.Context, body infra.PatchTagsRequest) error {
		return callInfra[infra.SubnetResponse](ctx, func(ctx context.Context) (*http.Response, error) {
			if err := checkSegments(vpcName, subnetName); err != nil {
				return nil, err
			}
			return c.infraAPI().PatchSubnet(ctx, vpcName, subnetName, &infra.PatchSubnetParams{XTenant: c.tenant()},
				infra.PatchSubnetJSONRequestBody(body), c.infraEditor())
		}, nil)
	}
	return reconcileTags(ctx, c, subnetKind, subnetName, patch, current, desired,
		func(ctx context.Context) (Subnet, error) { return c.GetSubnet(ctx, vpcName, subnetName) })
}

func (c *Runtime) DeleteSubnet(ctx context.Context, vpcName, subnetName string) error {
	return callInfra[infra.DeleteSubnetResponse](ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(vpcName, subnetName); err != nil {
			return nil, err
		}
		return c.infraAPI().DeleteSubnet(ctx, vpcName, subnetName, &infra.DeleteSubnetParams{XTenant: c.tenant()}, c.infraEditor())
	}, nil)
}

func (c *Runtime) CreateVM(ctx context.Context, request CreateVMRequest) (VM, error) {
	enableLogging := request.EnableLogging
	body := infra.CreateVMJSONRequestBody{
		Name:          request.Name,
		SkuID:         request.SKUID,
		VpcID:         request.VPCID,
		Image:         optionalString(request.Image),
		Tags:          infraTags(request.Tags),
		Customization: infraCustomization(request.Customization),
		// The handwritten request always sent enableLogging, including false.
		EnableLogging: &enableLogging,
	}
	return createWithNameReservation(ctx, c, vmKind, request.Name,
		func(ctx context.Context) error {
			return callInfra[infra.CreateVMResponse](ctx, func(ctx context.Context) (*http.Response, error) {
				return c.infraAPI().CreateVM(ctx, &infra.CreateVMParams{XTenant: c.tenant()}, body, c.infraEditor())
			}, nil)
		},
		c.vmReader(request.Name),
	)
}

// GetVM sorts attached volumes because the gateway returns them in map iteration order.
func (c *Runtime) GetVM(ctx context.Context, name string) (VM, error) {
	var wire infra.VMResponse
	err := callInfra(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(name); err != nil {
			return nil, err
		}
		return c.infraAPI().GetVM(ctx, name, &infra.GetVMParams{XTenant: c.tenant()}, c.infraEditor())
	}, &wire)
	if err != nil {
		return VM{}, err
	}
	return VM{
		URN:             value(wire.Urn),
		Name:            value(wire.Name),
		VPCID:           value(wire.VpcID),
		SKU:             skuFromInfra(wire.Sku),
		Status:          value(wire.Status),
		LastError:       value(wire.LastError),
		Tags:            tagsFromInfra(wire.Tags),
		AttachedVolumes: sortedStrings(wire.AttachedVolumes),
		OS:              osFromInfra(wire.Os),
		EnableLogging:   value(wire.EnableLogging),
	}, nil
}

// UpdateVMTags patches tags and reads back the resulting set.
func (c *Runtime) UpdateVMTags(ctx context.Context, name string, current, desired []Tag) (VM, error) {
	patch := func(ctx context.Context, body infra.PatchTagsRequest) error {
		return callInfra[infra.VMResponse](ctx, func(ctx context.Context) (*http.Response, error) {
			if err := checkSegments(name); err != nil {
				return nil, err
			}
			return c.infraAPI().PatchVM(ctx, name, &infra.PatchVMParams{XTenant: c.tenant()}, body, c.infraEditor())
		}, nil)
	}
	return reconcileTags(ctx, c, vmKind, name, patch, current, desired, c.vmReader(name))
}

func (c *Runtime) DeleteVM(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, vmKind, name,
		func(ctx context.Context) error {
			return callInfra[infra.DeleteVMResponse](ctx, func(ctx context.Context) (*http.Response, error) {
				if err := checkSegments(name); err != nil {
					return nil, err
				}
				return c.infraAPI().DeleteVM(ctx, name, &infra.DeleteVMParams{XTenant: c.tenant()}, c.infraEditor())
			}, nil)
		},
		c.vmReader(name),
	)
}

func (c *Runtime) vpcReader(name string) readFunc[VPC] {
	return func(ctx context.Context) (VPC, error) { return c.GetVPC(ctx, name) }
}

func (c *Runtime) vmReader(name string) readFunc[VM] {
	return func(ctx context.Context) (VM, error) { return c.GetVM(ctx, name) }
}

func vpcFromInfra(wire infra.VPCResponse) VPC {
	return VPC{
		ID:        value(wire.ID),
		URN:       value(wire.Urn),
		Name:      value(wire.Name),
		CIDR:      value(wire.Cidr),
		Status:    value(wire.Status),
		LastError: value(wire.LastError),
		Subnets:   subnetsFromInfra(wire.Subnets),
		Tags:      tagsFromInfra(wire.Tags),
	}
}

func subnetsFromInfra(wire *[]infra.SubnetResponse) []Subnet {
	if wire == nil {
		return nil
	}
	subnets := make([]Subnet, len(*wire))
	for i, subnet := range *wire {
		subnets[i] = Subnet{
			ID:        value(subnet.ID),
			URN:       value(subnet.Urn),
			Name:      value(subnet.Name),
			CIDR:      value(subnet.Cidr),
			Type:      value(subnet.Type),
			VPCID:     value(subnet.VpcID),
			Status:    value(subnet.Status),
			LastError: value(subnet.LastError),
			Tags:      tagsFromInfra(subnet.Tags),
		}
	}
	return subnets
}

func terminalStateError(kind, name, status, lastError string) error {
	if !strings.EqualFold(status, "error") {
		return nil
	}
	if lastError == "" {
		lastError = "no lastError was provided"
	}
	return fmt.Errorf("%s %q entered error state: %s", kind, name, lastError)
}

func (c *Runtime) waitPoll(ctx context.Context) error {
	timer := time.NewTimer(c.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
