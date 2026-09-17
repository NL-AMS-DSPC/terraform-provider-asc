package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// statusDeleting is the one status every infra-gateway read model reports while
// a deletion is still in flight. A resource in this status is a leftover of the
// previous generation, never the resource the caller just asked for.
const statusDeleting = "deleting"

// resourceView is the part of an infra-gateway resource that the shared
// lifecycle helpers need. Every read model reports the same status, lastError
// and tags triple, so one set of waiters serves VPCs, subnets, virtual
// machines, virtual machine groups and volumes alike.
type resourceView interface {
	lifecycle() (status string, lastError string)
	tags() []Tag
}

// readFunc reads the current server-side state of one already identified
// resource. It must return ErrResourceNotFound, and only that, when the
// resource is absent: the waiters treat every other error as fatal.
type readFunc[T resourceView] func(context.Context) (T, error)

// waitUntil polls read until done reports the resource has reached the caller's
// goal. Absence is treated as "not there yet", which is what every caller that
// waits for a resource to appear or to settle needs; waitForAbsent handles the
// opposite direction. A terminal error status aborts the wait immediately so a
// failed provisioning surfaces the gateway's own lastError instead of timing
// out. goal names the wait in the deadline message, for example "become
// available".
func waitUntil[T resourceView](ctx context.Context, c *Runtime, kind, name string, read readFunc[T], done func(T) bool, goal string) (T, error) {
	var zero T
	for {
		current, err := read(ctx)
		switch {
		case err == nil:
			status, lastError := current.lifecycle()
			if stateErr := terminalStateError(kind, name, status, lastError); stateErr != nil {
				return zero, stateErr
			}
			if done(current) {
				return current, nil
			}
		case !errors.Is(err, ErrResourceNotFound):
			return zero, fmt.Errorf("read %s %q: %w", kind, name, err)
		}
		if err := c.waitPoll(ctx); err != nil {
			return zero, fmt.Errorf("wait for %s %q to %s: %w", kind, name, goal, err)
		}
	}
}

// waitForPresent polls until the resource exists and is not a leftover of a
// previous generation still being deleted.
func waitForPresent[T resourceView](ctx context.Context, c *Runtime, kind, name string, read readFunc[T]) (T, error) {
	return waitUntil(ctx, c, kind, name, read, func(current T) bool {
		status, _ := current.lifecycle()
		return !strings.EqualFold(status, statusDeleting)
	}, "become available")
}

// waitForAbsent polls until the resource is authoritatively gone. Only a 404
// counts as absence: a read error would otherwise be mistaken for a completed
// deletion and drop a live resource out of state.
func waitForAbsent[T resourceView](ctx context.Context, c *Runtime, kind, name string, read readFunc[T]) error {
	for {
		current, err := read(ctx)
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for %s %q deletion: %w", kind, name, err)
		}
		status, lastError := current.lifecycle()
		if stateErr := terminalStateError(kind, name, status, lastError); stateErr != nil {
			return stateErr
		}
		if err := c.waitPoll(ctx); err != nil {
			return fmt.Errorf("wait for %s %q deletion: %w", kind, name, err)
		}
	}
}

// createWithNameReservation creates a resource whose name is held in a
// reservation table that outlives the read projection, and waits for the result.
//
// infra-gateway rejects a duplicate name with a typed 409 for exactly the
// resources that use this helper. A 409 whose resource is already gone from the
// read model, or is still being deleted, is a stale reservation from the
// generation Terraform just replaced, so the create is retried until the name is
// released. A 409 for a resource that is present and not deleting is a genuine
// collision with something Terraform does not own and is reported as is. Only
// the typed 409 makes the retry safe; an untyped 500 could mean the create
// partially succeeded, so callers whose conflicts are not typed must not use
// this helper.
func createWithNameReservation[T resourceView](ctx context.Context, c *Runtime, kind, name string, create func(context.Context) error, read readFunc[T]) (T, error) {
	var zero T
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	for {
		err := create(operationCtx)
		if err == nil {
			return waitForPresent(operationCtx, c, kind, name, read)
		}
		if !errors.Is(err, ErrResourceConflict) {
			return zero, err
		}

		current, getErr := read(operationCtx)
		switch {
		case getErr == nil:
			if status, _ := current.lifecycle(); !strings.EqualFold(status, statusDeleting) {
				return zero, fmt.Errorf("create %s %q: %w", kind, name, err)
			}
			if waitErr := waitForAbsent(operationCtx, c, kind, name, read); waitErr != nil {
				return zero, waitErr
			}
		case errors.Is(getErr, ErrResourceNotFound):
			// The read projector is absent but the independent name
			// reservation is stale; retry until it is released.
		default:
			return zero, fmt.Errorf("inspect conflicting %s %q: %w", kind, name, getErr)
		}

		if err := c.waitPoll(operationCtx); err != nil {
			return zero, fmt.Errorf("retry %s %q after conflict: %w", kind, name, err)
		}
	}
}

// deleteAndWait issues a delete and waits for the resource to disappear. A
// delete against something that is already gone is the desired end state, not a
// failure.
func deleteAndWait[T resourceView](ctx context.Context, c *Runtime, kind, name string, remove func(context.Context) error, read readFunc[T]) error {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := remove(operationCtx); err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		return err
	}
	return waitForAbsent(operationCtx, c, kind, name, read)
}
