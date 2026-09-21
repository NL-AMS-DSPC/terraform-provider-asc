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
	// An infra-gateway URN is asc:<org>:<domain>:<tenant>:<resource-type>:<resource-id>.
	urnParts = 6
)

// vmIdentity accepts the instance UUID reported by the volume projection and the VM name
// documented by the API.
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

// urnResourceID returns an empty string for unknown URN formats, leaving attachment
// matching to the VM name.
func urnResourceID(urn string) string {
	parts := strings.Split(urn, ":")
	if len(parts) != urnParts || parts[0] != "asc" {
		return ""
	}
	return parts[urnParts-1]
}

// AttachVolume waits for the volume projection to confirm the attachment; the POST
// response only acknowledges the command.
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

// DetachVolume waits for attachedToVM to clear. A missing volume or VM also means the
// attachment is gone.
func (c *Runtime) DetachVolume(ctx context.Context, volumeName, vmName string) error {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.delete(operationCtx, attachmentPath(volumeName, vmName)); err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		return fmt.Errorf("detach volume %q from VM %q: %w", volumeName, vmName, err)
	}

	// Unlike waitUntil, detachment treats a missing volume as completion.
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

// GetVolumeAttachment returns ErrResourceNotFound if either endpoint is absent or the
// volume is attached elsewhere.
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
