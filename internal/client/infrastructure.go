package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	vpcKind          = "VPC"
	subnetKind       = "subnet"
	vmKind           = "VM"
	ipAllocationKind = "IPAllocation"
)

func (c *Runtime) CreateVPC(ctx context.Context, request CreateVPCRequest) (VPC, error) {
	return createWithNameReservation(ctx, c, vpcKind, request.Name,
		func(ctx context.Context) error { return c.post(ctx, svcInfraGateway, "/vpcs/", request) },
		c.vpcReader(request.Name),
	)
}

func (c *Runtime) GetVPC(ctx context.Context, name string) (VPC, error) {
	var result VPC
	err := c.get(ctx, svcInfraGateway, vpcPath(name), &result)
	return result, err
}

// UpdateVPCTags patches tags and reads back the resulting set.
func (c *Runtime) UpdateVPCTags(ctx context.Context, name string, current, desired []Tag) (VPC, error) {
	return reconcileTags(ctx, c, vpcKind, name, vpcPath(name), current, desired, c.vpcReader(name))
}

func (c *Runtime) DeleteVPC(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, vpcKind, name,
		func(ctx context.Context) error { return c.delete(ctx, svcInfraGateway, vpcPath(name)) },
		c.vpcReader(name),
	)
}

func (c *Runtime) CreateSubnet(ctx context.Context, vpcName string, request CreateSubnetRequest) (Subnet, error) {
	if err := c.post(ctx, svcInfraGateway, subnetsPath(vpcName), request); err != nil {
		return Subnet{}, err
	}
	return c.GetSubnet(ctx, vpcName, request.Name)
}

func (c *Runtime) ListSubnets(ctx context.Context, vpcName string) ([]Subnet, error) {
	var result []Subnet
	err := c.get(ctx, svcInfraGateway, subnetsPath(vpcName), &result)
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
	return c.delete(ctx, svcInfraGateway, subnetPath(vpcName, subnetName))
}

func (c *Runtime) CreateVM(ctx context.Context, request CreateVMRequest) (VM, error) {
	return createWithNameReservation(ctx, c, vmKind, request.Name,
		func(ctx context.Context) error { return c.post(ctx, svcInfraGateway, "/vms/", request) },
		c.vmReader(request.Name),
	)
}

// GetVM sorts attached volumes because the gateway returns them in map iteration order,
// and public IPs by address so their order cannot change state either.
func (c *Runtime) GetVM(ctx context.Context, name string) (VM, error) {
	var result VM
	if err := c.get(ctx, svcInfraGateway, vmPath(name), &result); err != nil {
		return VM{}, err
	}
	slices.Sort(result.AttachedVolumes)
	slices.SortFunc(result.Addresses.PublicIPs, CompareAddresses)
	return result, nil
}

// UpdateVMTags patches tags and reads back the resulting set.
func (c *Runtime) UpdateVMTags(ctx context.Context, name string, current, desired []Tag) (VM, error) {
	return reconcileTags(ctx, c, vmKind, name, vmPath(name), current, desired, c.vmReader(name))
}

func (c *Runtime) DeleteVM(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, vmKind, name,
		func(ctx context.Context) error { return c.delete(ctx, svcInfraGateway, vmPath(name)) },
		c.vmReader(name),
	)
}

// AllocateIP waits for the VM to list the address among its public IPs
func (c *Runtime) AllocateIP(ctx context.Context, vmName, ip string) (VM, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.post(operationCtx, svcInfraGateway, vmIPPath(vmName), IPAllocationRequest{IP: ip}); err != nil {
		return VM{}, fmt.Errorf("allocate IP %q to VM %q: %w", ip, vmName, err)
	}
	return waitUntil(operationCtx, c, ipAllocationKind, ipAllocationName(vmName, ip), c.vmReader(vmName),
		func(current VM) bool { return slices.Contains(current.Addresses.PublicIPs, ip) }, "report the IP allocated")
}

// DeallocateIP waits for the VM to stop listing the address. A missing VM also means the allocation is gone.
func (c *Runtime) DeallocateIP(ctx context.Context, vmName, ip string) error {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	err := c.request(operationCtx, svcInfraGateway, http.MethodDelete, vmIPPath(vmName), IPDeallocationRequest{IP: ip}, nil)
	if errors.Is(err, ErrResourceNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deallocate IP %q from VM %q: %w", ip, vmName, err)
	}

	name := ipAllocationName(vmName, ip)
	for {
		current, err := c.GetVM(operationCtx, vmName)
		if errors.Is(err, ErrResourceNotFound) {
			return nil // no VM, no problem
		}
		if err != nil {
			return fmt.Errorf("wait for %s to deallocate: %w", name, err)
		}
		if stateErr := terminalStateError(ipAllocationKind, name, current.Status, current.LastError); stateErr != nil {
			return stateErr
		}
		if !slices.Contains(current.Addresses.PublicIPs, ip) {
			return nil
		}
		if err := c.waitPoll(operationCtx); err != nil {
			return fmt.Errorf("wait for %s to deallocate: %w", name, err)
		}
	}
}

// GetIPAllocation returns ErrResourceNotFound if the VM is gone or no longer lists the address
func (c *Runtime) GetIPAllocation(ctx context.Context, vmName, ip string) (VM, error) {
	current, err := c.GetVM(ctx, vmName)
	if err != nil {
		return VM{}, err
	}
	if !slices.Contains(current.Addresses.PublicIPs, ip) {
		return VM{}, fmt.Errorf("%w: IP %q is not allocated to VM %q", ErrResourceNotFound, ip, vmName)
	}
	return current, nil
}

func (c *Runtime) vpcReader(name string) readFunc[VPC] {
	return func(ctx context.Context) (VPC, error) { return c.GetVPC(ctx, name) }
}

func (c *Runtime) vmReader(name string) readFunc[VM] {
	return func(ctx context.Context) (VM, error) { return c.GetVM(ctx, name) }
}

func vpcPath(name string) string { return "/vpcs/" + escapedPathSegment(name) }

func vmPath(name string) string { return "/vms/" + escapedPathSegment(name) }

func vmIPPath(name string) string { return vmPath(name) + "/ip" }

func ipAllocationName(vmName, ip string) string { return vmName + ":" + ip }

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
