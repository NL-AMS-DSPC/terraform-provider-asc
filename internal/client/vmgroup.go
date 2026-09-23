package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

const vmGroupsPath = "/vm-groups/"

// VMGroup is the gateway read model. VPC ID, image and customization are write-only and
// must be preserved from configuration.
type VMGroup struct {
	URN               string            `json:"urn"`
	Name              string            `json:"name"`
	SKU               SKU               `json:"sku"`
	Status            string            `json:"status"`
	LastError         string            `json:"lastError"`
	Tags              []Tag             `json:"tags"`
	AttachedVolumes   []string          `json:"attachedVolumes"`
	AutoscalingPolicy AutoscalingPolicy `json:"autoscalingPolicy"`
}

// AutoscalingPolicy is the scaling configuration of a virtual machine group.
// The gateway spells the replica bounds in snake case and the cron rule's
// desired replicas in camel case; both request and response use these names.
type AutoscalingPolicy struct {
	MinReplicas int64     `json:"min_replicas"`
	MaxReplicas int64     `json:"max_replicas"`
	Cron        *CronRule `json:"cron,omitempty"`
}

// CronRule must be omitted when absent; an empty object fails gateway validation.
type CronRule struct {
	Timezone        string `json:"timezone"`
	Start           string `json:"start"`
	End             string `json:"end"`
	DesiredReplicas int64  `json:"desiredReplicas"`
}

// CreateVMGroupRequest sends the required policy as autoscalers; reads call it
// autoscalingPolicy.
type CreateVMGroupRequest struct {
	Name              string            `json:"name"`
	SKUID             string            `json:"skuID"`
	VPCID             string            `json:"vpcID"`
	Image             string            `json:"image,omitempty"`
	AutoscalingPolicy AutoscalingPolicy `json:"autoscalers"`
	Tags              []Tag             `json:"tags,omitempty"`
	Customization     *Customization    `json:"customization,omitempty"`
}

func (g VMGroup) lifecycle() (string, string) { return g.Status, g.LastError }

func (g VMGroup) tags() []Tag { return g.Tags }

// CreateVMGroup reports name conflicts immediately. Group and VM names share a reservation
// table, and group names are released only after deletion is acknowledged.
func (c *Runtime) CreateVMGroup(ctx context.Context, request CreateVMGroupRequest) (VMGroup, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.post(operationCtx, svcInfraGateway, vmGroupsPath, request); err != nil {
		if errors.Is(err, ErrResourceConflict) {
			return VMGroup{}, fmt.Errorf(
				"create VM group %q: the name is already in use for this tenant. "+
					"Virtual machines and virtual machine groups share one name reservation, "+
					"and infra-gateway holds a group's name until it acknowledges the group's "+
					"deletion, so a group Terraform destroyed moments ago may still hold it: %w",
				request.Name, err)
		}
		return VMGroup{}, err
	}
	return waitForPresent(operationCtx, c, "VM group", request.Name,
		func(ctx context.Context) (VMGroup, error) { return c.GetVMGroup(ctx, request.Name) })
}

// GetVMGroup sorts attached volumes because the gateway returns them in map iteration
// order.
func (c *Runtime) GetVMGroup(ctx context.Context, name string) (VMGroup, error) {
	var result VMGroup
	if err := c.get(ctx, svcInfraGateway, vmGroupsPath+escapedPathSegment(name), &result); err != nil {
		return VMGroup{}, err
	}
	slices.Sort(result.AttachedVolumes)
	return result, nil
}

// DeleteVMGroup waits for deletion so replacements can reuse the name.
func (c *Runtime) DeleteVMGroup(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, "VM group", name,
		func(ctx context.Context) error {
			return c.delete(ctx, svcInfraGateway, vmGroupsPath+escapedPathSegment(name))
		},
		func(ctx context.Context) (VMGroup, error) { return c.GetVMGroup(ctx, name) },
	)
}
