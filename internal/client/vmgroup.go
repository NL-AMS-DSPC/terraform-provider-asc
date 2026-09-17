package client

import (
	"context"
	"slices"
)

const vmGroupsPath = "/vm-groups/"

// VMGroup is the infra-gateway read model of a virtual machine group.
//
// The projection carries no vpcID, no image and no customization: those reach
// the gateway on the create command only and are never served back, so the
// provider has to treat them as configuration-only.
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

// CronRule is the optional cron window of an autoscaling policy. It is a
// pointer on the policy because the gateway requires all four fields as soon as
// the object is present, so an absent rule has to be omitted entirely.
type CronRule struct {
	Timezone        string `json:"timezone"`
	Start           string `json:"start"`
	End             string `json:"end"`
	DesiredReplicas int64  `json:"desiredReplicas"`
}

// CreateVMGroupRequest is the create body of a virtual machine group. The
// gateway names the autoscaling policy "autoscalers" on the way in and
// "autoscalingPolicy" on the way out, and the field is not optional, so it is
// always sent.
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

// CreateVMGroup creates a virtual machine group and waits for it to appear.
//
// Group names are reserved in the same table as virtual machine names, so a
// duplicate comes back as the typed ErrVMNameNotUnique, which the gateway maps
// to a 409. That typed conflict is what makes the shared name reservation
// helper safe to use here: a 409 can only mean the name is taken, never that
// the create half succeeded.
func (c *Runtime) CreateVMGroup(ctx context.Context, request CreateVMGroupRequest) (VMGroup, error) {
	return createWithNameReservation(ctx, c, "VM group", request.Name,
		func(ctx context.Context) error { return c.post(ctx, vmGroupsPath, request) },
		func(ctx context.Context) (VMGroup, error) { return c.GetVMGroup(ctx, request.Name) },
	)
}

// GetVMGroup reads one virtual machine group by name.
//
// The attached volumes are sorted before they leave the client. The gateway
// builds that slice by ranging over a Go map, so its order differs between two
// reads of an unchanged group and an unsorted list would surface as a diff that
// no configuration change can settle.
func (c *Runtime) GetVMGroup(ctx context.Context, name string) (VMGroup, error) {
	var result VMGroup
	if err := c.get(ctx, vmGroupsPath+escapedPathSegment(name), &result); err != nil {
		return VMGroup{}, err
	}
	slices.Sort(result.AttachedVolumes)
	return result, nil
}

// DeleteVMGroup deletes a virtual machine group and waits for it to be gone.
// The wait matters because the name reservation is only released once the
// deletion completes, and a replacement reuses that name.
func (c *Runtime) DeleteVMGroup(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, "VM group", name,
		func(ctx context.Context) error { return c.delete(ctx, vmGroupsPath+escapedPathSegment(name)) },
		func(ctx context.Context) (VMGroup, error) { return c.GetVMGroup(ctx, name) },
	)
}
