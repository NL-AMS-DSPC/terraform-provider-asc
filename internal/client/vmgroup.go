package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
)

// VMGroup is the gateway read model. VPC ID, image and customization are write-only and
// must be preserved from configuration.
type VMGroup struct {
	URN               string
	Name              string
	SKU               SKU
	Status            string
	LastError         string
	Tags              []Tag
	AttachedVolumes   []string
	AutoscalingPolicy AutoscalingPolicy
}

// AutoscalingPolicy is the scaling configuration of a virtual machine group.
// The gateway spells the replica bounds in snake case and the cron rule's
// desired replicas in camel case; both request and response use these names.
type AutoscalingPolicy struct {
	MinReplicas int64
	MaxReplicas int64
	Cron        *CronRule
}

// CronRule must be omitted when absent; an empty object fails gateway validation.
type CronRule struct {
	Timezone        string
	Start           string
	End             string
	DesiredReplicas int64
}

// CreateVMGroupRequest sends the required policy as autoscalers; reads call it
// autoscalingPolicy.
type CreateVMGroupRequest struct {
	Name              string
	SKUID             string
	VPCID             string
	Image             string
	AutoscalingPolicy AutoscalingPolicy
	Tags              []Tag
	Customization     *Customization
}

func (g VMGroup) lifecycle() (string, string) { return g.Status, g.LastError }

func (g VMGroup) tags() []Tag { return g.Tags }

// CreateVMGroup reports name conflicts immediately. Group and VM names share a reservation
// table, and group names are released only after deletion is acknowledged.
func (c *Runtime) CreateVMGroup(ctx context.Context, request CreateVMGroupRequest) (VMGroup, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	body := infra.CreateVMGroupJSONRequestBody{
		Name:  request.Name,
		SkuID: request.SKUID,
		VpcID: request.VPCID,
		Image: optionalString(request.Image),
		// The policy is required, so it is always sent.
		Autoscalers:   infraAutoscalingPolicy(request.AutoscalingPolicy),
		Tags:          infraTags(request.Tags),
		Customization: infraCustomization(request.Customization),
	}
	err := callInfra[infra.CreateVMGroupResponse](operationCtx, func(ctx context.Context) (*http.Response, error) {
		return c.infraAPI().CreateVMGroup(ctx, &infra.CreateVMGroupParams{XTenant: c.tenant()}, body, c.infraEditor())
	}, nil)
	if err != nil {
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
	var wire infra.VMGroupResponse
	err := callInfra(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(name); err != nil {
			return nil, err
		}
		return c.infraAPI().GetVMGroup(ctx, name, &infra.GetVMGroupParams{XTenant: c.tenant()}, c.infraEditor())
	}, &wire)
	if err != nil {
		return VMGroup{}, err
	}
	group := VMGroup{
		URN:             value(wire.Urn),
		Name:            value(wire.Name),
		SKU:             skuFromInfra(wire.Sku),
		Status:          value(wire.Status),
		LastError:       value(wire.LastError),
		Tags:            tagsFromInfra(wire.Tags),
		AttachedVolumes: sortedStrings(wire.AttachedVolumes),
	}
	if policy := wire.AutoscalingPolicy; policy != nil {
		group.AutoscalingPolicy = AutoscalingPolicy{MinReplicas: int64(policy.MinReplicas), MaxReplicas: int64(policy.MaxReplicas)}
		if cron := policy.Cron; cron != nil {
			group.AutoscalingPolicy.Cron = &CronRule{
				Timezone: cron.Timezone, Start: cron.Start, End: cron.End, DesiredReplicas: int64(cron.DesiredReplicas),
			}
		}
	}
	return group, nil
}

// DeleteVMGroup waits for deletion so replacements can reuse the name.
func (c *Runtime) DeleteVMGroup(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, "VM group", name,
		func(ctx context.Context) error {
			return callInfra[infra.DeleteVMGroupResponse](ctx, func(ctx context.Context) (*http.Response, error) {
				if err := checkSegments(name); err != nil {
					return nil, err
				}
				return c.infraAPI().DeleteVMGroup(ctx, name, &infra.DeleteVMGroupParams{XTenant: c.tenant()}, c.infraEditor())
			}, nil)
		},
		func(ctx context.Context) (VMGroup, error) { return c.GetVMGroup(ctx, name) },
	)
}

func infraAutoscalingPolicy(policy AutoscalingPolicy) *infra.AutoscalingPolicy {
	wire := &infra.AutoscalingPolicy{MinReplicas: int(policy.MinReplicas), MaxReplicas: int(policy.MaxReplicas)}
	if cron := policy.Cron; cron != nil {
		wire.Cron = &infra.CronRule{
			Timezone: cron.Timezone, Start: cron.Start, End: cron.End, DesiredReplicas: int(cron.DesiredReplicas),
		}
	}
	return wire
}
