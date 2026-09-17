package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (c *Runtime) CreateVPC(ctx context.Context, request CreateVPCRequest) (VPC, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()
	for {
		err := c.post(operationCtx, "/vpcs/", request)
		if err == nil {
			return c.waitForVPCPresent(operationCtx, request.Name)
		}
		if !errors.Is(err, ErrResourceConflict) {
			return VPC{}, err
		}

		current, getErr := c.GetVPC(operationCtx, request.Name)
		switch {
		case getErr == nil && !strings.EqualFold(current.Status, "deleting"):
			return VPC{}, fmt.Errorf("create VPC %q: %w", request.Name, err)
		case getErr == nil:
			if waitErr := c.waitForVPCAbsent(operationCtx, request.Name); waitErr != nil {
				return VPC{}, waitErr
			}
		case errors.Is(getErr, ErrResourceNotFound):
			// The read projector is absent but the independent name reservation
			// is stale. Only the typed 409 makes this retry safe.
		default:
			return VPC{}, fmt.Errorf("inspect conflicting VPC %q: %w", request.Name, getErr)
		}
		if err := c.waitPoll(operationCtx); err != nil {
			return VPC{}, fmt.Errorf("retry VPC %q after conflict: %w", request.Name, err)
		}
	}
}

func (c *Runtime) GetVPC(ctx context.Context, name string) (VPC, error) {
	var result VPC
	err := c.get(ctx, "/vpcs/"+escapedPathSegment(name), &result)
	return result, err
}

func (c *Runtime) DeleteVPC(ctx context.Context, name string) error {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()
	if err := c.delete(operationCtx, "/vpcs/"+escapedPathSegment(name)); err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		return err
	}
	return c.waitForVPCAbsent(operationCtx, name)
}

func (c *Runtime) CreateSubnet(ctx context.Context, vpcName string, request CreateSubnetRequest) (Subnet, error) {
	if err := c.post(ctx, fmt.Sprintf("/vpcs/%s/subnets/", escapedPathSegment(vpcName)), request); err != nil {
		return Subnet{}, err
	}
	return c.GetSubnet(ctx, vpcName, request.Name)
}

func (c *Runtime) ListSubnets(ctx context.Context, vpcName string) ([]Subnet, error) {
	var result []Subnet
	err := c.get(ctx, fmt.Sprintf("/vpcs/%s/subnets/", escapedPathSegment(vpcName)), &result)
	return result, err
}

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

func (c *Runtime) DeleteSubnet(ctx context.Context, vpcName, subnetName string) error {
	return c.delete(ctx, fmt.Sprintf("/vpcs/%s/subnets/%s", escapedPathSegment(vpcName), escapedPathSegment(subnetName)))
}

func (c *Runtime) CreateVM(ctx context.Context, request CreateVMRequest) (VM, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()
	for {
		err := c.post(operationCtx, "/vms/", request)
		if err == nil {
			return c.waitForVMPresent(operationCtx, request.Name)
		}
		if !errors.Is(err, ErrResourceConflict) {
			return VM{}, err
		}

		current, getErr := c.GetVM(operationCtx, request.Name)
		switch {
		case getErr == nil && !strings.EqualFold(current.Status, "deleting"):
			return VM{}, fmt.Errorf("create VM %q: %w", request.Name, err)
		case getErr == nil:
			if waitErr := c.waitForVMAbsent(operationCtx, request.Name); waitErr != nil {
				return VM{}, waitErr
			}
		case errors.Is(getErr, ErrResourceNotFound):
			// Retry only the typed stale-reservation conflict.
		default:
			return VM{}, fmt.Errorf("inspect conflicting VM %q: %w", request.Name, getErr)
		}
		if err := c.waitPoll(operationCtx); err != nil {
			return VM{}, fmt.Errorf("retry VM %q after conflict: %w", request.Name, err)
		}
	}
}

func (c *Runtime) GetVM(ctx context.Context, name string) (VM, error) {
	var result VM
	err := c.get(ctx, "/vms/"+escapedPathSegment(name), &result)
	return result, err
}

func (c *Runtime) DeleteVM(ctx context.Context, name string) error {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()
	if err := c.delete(operationCtx, "/vms/"+escapedPathSegment(name)); err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		return err
	}
	return c.waitForVMAbsent(operationCtx, name)
}

func (c *Runtime) waitForVPCPresent(ctx context.Context, name string) (VPC, error) {
	for {
		current, err := c.GetVPC(ctx, name)
		if err == nil {
			if stateErr := terminalStateError("VPC", name, current.Status, current.LastError); stateErr != nil {
				return VPC{}, stateErr
			}
			if !strings.EqualFold(current.Status, "deleting") {
				return current, nil
			}
			if err := c.waitPoll(ctx); err != nil {
				return VPC{}, fmt.Errorf("wait for created VPC %q: %w", name, err)
			}
			continue
		}
		if !errors.Is(err, ErrResourceNotFound) {
			return VPC{}, fmt.Errorf("read created VPC %q: %w", name, err)
		}
		if err := c.waitPoll(ctx); err != nil {
			return VPC{}, fmt.Errorf("wait for created VPC %q: %w", name, err)
		}
	}
}

func (c *Runtime) waitForVMPresent(ctx context.Context, name string) (VM, error) {
	for {
		current, err := c.GetVM(ctx, name)
		if err == nil {
			if stateErr := terminalStateError("VM", name, current.Status, current.LastError); stateErr != nil {
				return VM{}, stateErr
			}
			if !strings.EqualFold(current.Status, "deleting") {
				return current, nil
			}
			if err := c.waitPoll(ctx); err != nil {
				return VM{}, fmt.Errorf("wait for created VM %q: %w", name, err)
			}
			continue
		}
		if !errors.Is(err, ErrResourceNotFound) {
			return VM{}, fmt.Errorf("read created VM %q: %w", name, err)
		}
		if err := c.waitPoll(ctx); err != nil {
			return VM{}, fmt.Errorf("wait for created VM %q: %w", name, err)
		}
	}
}

func (c *Runtime) waitForVPCAbsent(ctx context.Context, name string) error {
	for {
		current, err := c.GetVPC(ctx, name)
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for VPC %q deletion: %w", name, err)
		}
		if stateErr := terminalStateError("VPC", name, current.Status, current.LastError); stateErr != nil {
			return stateErr
		}
		if err := c.waitPoll(ctx); err != nil {
			return fmt.Errorf("wait for VPC %q deletion: %w", name, err)
		}
	}
}

func (c *Runtime) waitForVMAbsent(ctx context.Context, name string) error {
	for {
		current, err := c.GetVM(ctx, name)
		if errors.Is(err, ErrResourceNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for VM %q deletion: %w", name, err)
		}
		if stateErr := terminalStateError("VM", name, current.Status, current.LastError); stateErr != nil {
			return stateErr
		}
		if err := c.waitPoll(ctx); err != nil {
			return fmt.Errorf("wait for VM %q deletion: %w", name, err)
		}
	}
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
