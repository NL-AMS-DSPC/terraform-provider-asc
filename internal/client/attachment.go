package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	attachmentKind        = "volume attachment"
	statusVolumeAttached  = "attached"
	statusVolumeDetaching = "detaching"
	// urnParts is the number of colon separated segments in an infra-gateway
	// urn, asc:<org>:<domain>:<tenant>:<resource-type>:<resource-id>.
	urnParts = 6
)

// vmIdentity is the set of spellings by which infra-gateway may name the
// machine a volume is attached to.
//
// The volume read model reports attachedToVM from
// VMAttachedToVolumeAcknowledgedEvent.InstanceID, a uuid, and drops the VMName
// the same event carries (infra-gateway cqrs/queries/get_volume/query.go). The
// field is nonetheless documented and named as if it held a machine name, so
// the provider accepts either spelling rather than betting on one: the uuid is
// what a live gateway sends today, and the name is what the field claims to be.
type vmIdentity struct {
	name string
	id   string
}

// matches reports whether attachedToVM refers to this machine. An empty value
// means the volume is attached to nothing.
func (v vmIdentity) matches(attachedToVM string) bool {
	if attachedToVM == "" {
		return false
	}
	return attachedToVM == v.name || (v.id != "" && attachedToVM == v.id)
}

// resolveVM reads the machine so its instance id is known before anything is
// compared against the volume's attachedToVM.
func (c *Runtime) resolveVM(ctx context.Context, vmName string) (vmIdentity, error) {
	vm, err := c.GetVM(ctx, vmName)
	if err != nil {
		return vmIdentity{}, err
	}
	return vmIdentity{name: vm.Name, id: urnResourceID(vm.URN)}, nil
}

// urnResourceID returns the resource id of a well formed infra-gateway urn and
// an empty string for anything else, so a urn the gateway changes the shape of
// degrades to a name-only match rather than a wrong one.
func urnResourceID(urn string) string {
	parts := strings.Split(urn, ":")
	if len(parts) != urnParts || parts[0] != "asc" {
		return ""
	}
	return parts[urnParts-1]
}

// AttachVolume attaches a volume to a virtual machine and waits for the
// attachment to complete.
//
// The route answers with an acknowledgement only: the handler dispatches a
// command and returns {volumeName, vmName} without waiting for anything. The
// volume's own read model is the single place completion becomes visible, so
// the wait watches its status and attachedToVM rather than trusting the 200.
func (c *Runtime) AttachVolume(ctx context.Context, volumeName, vmName string) (Volume, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	machine, err := c.resolveVM(operationCtx, vmName)
	if err != nil {
		return Volume{}, fmt.Errorf("attach volume %q to VM %q: %w", volumeName, vmName, err)
	}

	if err := c.post(operationCtx, attachmentPath(volumeName, vmName), nil); err != nil {
		return Volume{}, fmt.Errorf("attach volume %q to VM %q: %w", volumeName, vmName, err)
	}
	return waitUntil(operationCtx, c, attachmentKind, attachmentName(volumeName, vmName), c.volumeReader(volumeName),
		func(current Volume) bool {
			return strings.EqualFold(current.Status, statusVolumeAttached) && machine.matches(current.AttachedToVM)
		}, "report the volume attached")
}

// DetachVolume detaches a volume from a virtual machine and waits for the
// detachment to complete.
//
// A volume or machine that is already gone means the attachment cannot exist,
// which is the desired end state for a destroy, so an authoritative 404 is not
// an error. Completion is again read from the volume: the gateway clears
// attachedToVM once the detachment lands, so this direction needs no identity
// comparison.
func (c *Runtime) DetachVolume(ctx context.Context, volumeName, vmName string) error {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.delete(operationCtx, attachmentPath(volumeName, vmName)); err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		return fmt.Errorf("detach volume %q from VM %q: %w", volumeName, vmName, err)
	}

	// waitUntil treats absence as "not there yet", which is right when waiting
	// for something to appear and wrong here: a volume that has been deleted
	// out of band cannot be attached to anything, so the loop is written out
	// rather than reusing it.
	name := attachmentName(volumeName, vmName)
	for {
		current, err := c.GetVolume(operationCtx, volumeName)
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for %s to detach: %w", name, err)
		}
		if stateErr := terminalStateError(attachmentKind, name, current.Status, current.LastError); stateErr != nil {
			return stateErr
		}
		if current.AttachedToVM == "" && !strings.EqualFold(current.Status, statusVolumeDetaching) {
			return nil
		}
		if err := c.waitPoll(operationCtx); err != nil {
			return fmt.Errorf("wait for %s to detach: %w", name, err)
		}
	}
}

// GetVolumeAttachment reports the attachment of one volume to one machine.
// ErrResourceNotFound means the volume is absent, the machine is absent, or the
// volume is attached elsewhere, which is the only thing that removes the
// attachment from Terraform state.
func (c *Runtime) GetVolumeAttachment(ctx context.Context, volumeName, vmName string) (Volume, error) {
	machine, err := c.resolveVM(ctx, vmName)
	if err != nil {
		return Volume{}, err
	}
	current, err := c.GetVolume(ctx, volumeName)
	if err != nil {
		return Volume{}, err
	}
	if !machine.matches(current.AttachedToVM) {
		return Volume{}, fmt.Errorf("%w: volume %q is not attached to VM %q", ErrResourceNotFound, volumeName, vmName)
	}
	return current, nil
}

func attachmentPath(volumeName, vmName string) string {
	return volumePath(volumeName) + "/attach/" + escapedPathSegment(vmName)
}

func attachmentName(volumeName, vmName string) string {
	return volumeName + " on " + vmName
}
