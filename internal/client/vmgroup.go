package client

import (
	"context"
	"errors"
	"fmt"
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
// createWithNameReservation is deliberately not used. That helper retries a 409
// whose resource is absent from the read model, on the assumption the name is
// held by a reservation the gateway is about to release. Group names are
// reserved in the same vm_names_in_use table as virtual machine names, and a
// group's row is only released once infra-gateway acknowledges its deletion, so
// a 409 here is a name that is genuinely taken now: by a live group, by a
// virtual machine sharing the table, or by a group whose deletion has not been
// acknowledged yet. Retrying would poll until the lifecycle timeout and then
// report a deadline instead of the reason, so the conflict is reported at once.
//
// A 409 on this route can mean nothing else: the gateway's CreateVMGroup
// handler answers every other failure with a 400, and respondWithServiceError
// maps only ErrVPCNameNotUnique, which this path cannot raise, and
// ErrVMNameNotUnique to 409.
func (c *Runtime) CreateVMGroup(ctx context.Context, request CreateVMGroupRequest) (VMGroup, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.post(operationCtx, vmGroupsPath, request); err != nil {
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
// The wait matters because infra-gateway releases the name reservation only
// when it acknowledges the deletion, and a replacement reuses that name.
func (c *Runtime) DeleteVMGroup(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, "VM group", name,
		func(ctx context.Context) error { return c.delete(ctx, vmGroupsPath+escapedPathSegment(name)) },
		func(ctx context.Context) (VMGroup, error) { return c.GetVMGroup(ctx, name) },
	)
}
