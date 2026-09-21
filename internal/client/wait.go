package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A deleting projection belongs to the previous resource generation.
const statusDeleting = "deleting"

// resourceView provides the fields shared by lifecycle and tag waiters.
type resourceView interface {
	lifecycle() (status string, lastError string)
	tags() []Tag
}

// readFunc must report absence as ErrResourceNotFound; other errors abort polling.
type readFunc[T resourceView] func(context.Context) (T, error)

// waitUntil polls until done succeeds. Absence means not ready; a terminal resource error
// aborts with lastError.
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

// waitForAbsent requires authoritative absence; transport errors must not remove a live
// resource from state.
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

// createWithNameReservation retries typed 409 conflicts while the old projection is absent
// or deleting. A live resource is a real collision. Untyped errors are never retried
// because the create may have partially succeeded.
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

// deleteAndWait accepts an already absent resource and otherwise waits for deletion.
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
