package client

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	vpcKind    = "VPC"
	subnetKind = "subnet"
	vmKind     = "VM"
)

func (c *Runtime) CreateVPC(ctx context.Context, request CreateVPCRequest) (VPC, error) {
	return createWithNameReservation(ctx, c, vpcKind, request.Name,
		func(ctx context.Context) error { return c.post(ctx, "/vpcs/", request) },
		c.vpcReader(request.Name),
	)
}

func (c *Runtime) GetVPC(ctx context.Context, name string) (VPC, error) {
	var result VPC
	err := c.get(ctx, vpcPath(name), &result)
	return result, err
}

// UpdateVPCTags patches tags and reads back the resulting set.
func (c *Runtime) UpdateVPCTags(ctx context.Context, name string, current, desired []Tag) (VPC, error) {
	return reconcileTags(ctx, c, vpcKind, name, vpcPath(name), current, desired, c.vpcReader(name))
}

func (c *Runtime) DeleteVPC(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, vpcKind, name,
		func(ctx context.Context) error { return c.delete(ctx, vpcPath(name)) },
		c.vpcReader(name),
	)
}

func (c *Runtime) CreateSubnet(ctx context.Context, vpcName string, request CreateSubnetRequest) (Subnet, error) {
	if err := c.post(ctx, subnetsPath(vpcName), request); err != nil {
		return Subnet{}, err
	}
	return c.GetSubnet(ctx, vpcName, request.Name)
}

func (c *Runtime) ListSubnets(ctx context.Context, vpcName string) ([]Subnet, error) {
	var result []Subnet
	err := c.get(ctx, subnetsPath(vpcName), &result)
	return result, err
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
	return reconcileTags(ctx, c, subnetKind, subnetName, subnetPath(vpcName, subnetName), current, desired,
		func(ctx context.Context) (Subnet, error) { return c.GetSubnet(ctx, vpcName, subnetName) })
}

func (c *Runtime) DeleteSubnet(ctx context.Context, vpcName, subnetName string) error {
	return c.delete(ctx, subnetPath(vpcName, subnetName))
}

func (c *Runtime) CreateVM(ctx context.Context, request CreateVMRequest) (VM, error) {
	return createWithNameReservation(ctx, c, vmKind, request.Name,
		func(ctx context.Context) error { return c.post(ctx, "/vms/", request) },
		c.vmReader(request.Name),
	)
}

// GetVM sorts attached volumes because the gateway returns them in map iteration order.
func (c *Runtime) GetVM(ctx context.Context, name string) (VM, error) {
	var result VM
	if err := c.get(ctx, vmPath(name), &result); err != nil {
		return VM{}, err
	}
	slices.Sort(result.AttachedVolumes)
	return result, nil
}

// UpdateVMTags patches tags and reads back the resulting set.
func (c *Runtime) UpdateVMTags(ctx context.Context, name string, current, desired []Tag) (VM, error) {
	return reconcileTags(ctx, c, vmKind, name, vmPath(name), current, desired, c.vmReader(name))
}

func (c *Runtime) DeleteVM(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, vmKind, name,
		func(ctx context.Context) error { return c.delete(ctx, vmPath(name)) },
		c.vmReader(name),
	)
}

func (c *Runtime) vpcReader(name string) readFunc[VPC] {
	return func(ctx context.Context) (VPC, error) { return c.GetVPC(ctx, name) }
}

func (c *Runtime) vmReader(name string) readFunc[VM] {
	return func(ctx context.Context) (VM, error) { return c.GetVM(ctx, name) }
}

func vpcPath(name string) string { return "/vpcs/" + escapedPathSegment(name) }

func vmPath(name string) string { return "/vms/" + escapedPathSegment(name) }

func subnetsPath(vpcName string) string {
	return fmt.Sprintf("/vpcs/%s/subnets/", escapedPathSegment(vpcName))
}

func subnetPath(vpcName, subnetName string) string {
	return subnetsPath(vpcName) + escapedPathSegment(subnetName)
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
