package common

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// BootDiskSizeAttribute is the shared boot_disk_size_gb schema. It is Optional and Computed
// with a plan modifier rather than a schema default, because state written before the
// attribute existed holds null and a default would plan a replacement of every machine.
func BootDiskSizeAttribute(subject string) schema.Int64Attribute {
	return schema.Int64Attribute{
		Description: fmt.Sprintf("Size in GB of the boot disk created for each machine. Defaults to %d. "+
			"ASC never returns it, so the configured value is kept in state; state written before this attribute "+
			"existed counts as %d. Changing it replaces the %s.", client.DefaultBootDiskSizeGB, client.DefaultBootDiskSizeGB, subject),
		Optional:      true,
		Computed:      true,
		PlanModifiers: []planmodifier.Int64{bootDiskSize{}},
		Validators:    []validator.Int64{int64validator.AtLeast(1)},
	}
}

type bootDiskSize struct{}

func (bootDiskSize) Description(context.Context) string {
	return "Defaults to 32 on create, keeps the prior value when omitted, and replaces the resource only when a set value differs from the prior one, counting an absent prior value as 32."
}

func (m bootDiskSize) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (bootDiskSize) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	// Destroy.
	if req.Plan.Raw.IsNull() {
		return
	}
	// Create.
	if req.State.Raw.IsNull() {
		if req.ConfigValue.IsNull() {
			resp.PlanValue = types.Int64Value(client.DefaultBootDiskSizeGB)
		}
		return
	}
	if req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsNull() {
		// Keep whatever state holds, including null from before the attribute existed.
		resp.PlanValue = req.StateValue
		return
	}
	prior := int64(client.DefaultBootDiskSizeGB)
	if !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
		prior = req.StateValue.ValueInt64()
	}
	if req.ConfigValue.ValueInt64() != prior {
		resp.RequiresReplace = true
	}
}
