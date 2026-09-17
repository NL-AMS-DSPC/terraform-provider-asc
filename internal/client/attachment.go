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
)

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

	if err := c.post(operationCtx, attachmentPath(volumeName, vmName), nil); err != nil {
		return Volume{}, fmt.Errorf("attach volume %q to VM %q: %w", volumeName, vmName, err)
	}
	return waitUntil(operationCtx, c, attachmentKind, attachmentName(volumeName, vmName), c.volumeReader(volumeName),
		func(current Volume) bool {
			return strings.EqualFold(current.Status, statusVolumeAttached) && current.AttachedToVM == vmName
		}, "report the volume attached")
}

// DetachVolume detaches a volume from a virtual machine and waits for the
// detachment to complete.
//
// A volume or machine that is already gone means the attachment cannot exist,
// which is the desired end state for a destroy, so an authoritative 404 is not
// an error. Completion is again read from the volume: the gateway clears
// attachedToVM once the detachment lands.
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
// ErrResourceNotFound means the volume is absent or is attached elsewhere,
// which is the only thing that removes the attachment from Terraform state.
func (c *Runtime) GetVolumeAttachment(ctx context.Context, volumeName, vmName string) (Volume, error) {
	current, err := c.GetVolume(ctx, volumeName)
	if err != nil {
		return Volume{}, err
	}
	if current.AttachedToVM != vmName {
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
