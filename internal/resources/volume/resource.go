// Package volume implements the asc_volume resource.
package volume

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/common"
)

var (
	_ resource.Resource                = &Resource{}
	_ resource.ResourceWithConfigure   = &Resource{}
	_ resource.ResourceWithImportState = &Resource{}
	_ resource.ResourceWithModifyPlan  = &Resource{}
)

type resourceClient interface {
	CreateVolume(context.Context, client.CreateVolumeRequest) (client.Volume, error)
	GetVolume(context.Context, string) (client.Volume, error)
	ResizeVolume(context.Context, string, string) (client.Volume, error)
	UpdateVolumeTags(context.Context, string, []client.Tag, []client.Tag) (client.Volume, error)
	DeleteVolume(context.Context, string) error
}

type Resource struct {
	client resourceClient
}

// Model identifies the volume by name and retains the gateway URN.
type Model struct {
	Name         types.String `tfsdk:"name"`
	Size         types.String `tfsdk:"size"`
	SizeBytes    types.Int64  `tfsdk:"size_bytes"`
	StorageClass types.String `tfsdk:"storage_class"`
	AccessMode   types.String `tfsdk:"access_mode"`
	VolumeMode   types.String `tfsdk:"volume_mode"`
	URN          types.String `tfsdk:"urn"`
	Status       types.String `tfsdk:"status"`
	LastError    types.String `tfsdk:"last_error"`
	AttachedToVM types.String `tfsdk:"attached_to_vm"`
	Tags         types.Map    `tfsdk:"tags"`
	Labels       types.Map    `tfsdk:"labels"`
	Annotations  types.Map    `tfsdk:"annotations"`
}

func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		Description: "Manages a block volume through ASC infra-gateway.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "Name of the volume, which identifies it in infra-gateway.",
				Required:      true,
				PlanModifiers: replaceString,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 50)},
			},
			"size": schema.StringAttribute{
				Description: "Requested capacity as a Kubernetes quantity, for example \"10Gi\". " +
					"A larger value resizes the volume in place; infra-gateway cannot shrink one.",
				Required: true,
			},
			"size_bytes": schema.Int64Attribute{
				Description: "Capacity in bytes. infra-gateway may echo back a different but " +
					"numerically equal spelling of size, so this is the value to compare against.",
				Computed: true,
			},
			"storage_class": schema.StringAttribute{Description: "Storage class backing the volume.", Required: true, PlanModifiers: replaceString},
			"access_mode": schema.StringAttribute{
				Description:   "Access mode of the volume.",
				Required:      true,
				PlanModifiers: replaceString,
				Validators:    []validator.String{stringvalidator.OneOf("ReadWriteOnce", "ReadWriteMany", "ReadOnlyMany")},
			},
			"volume_mode":    schema.StringAttribute{Description: "Volume mode reported by infra-gateway.", Computed: true},
			"urn":            schema.StringAttribute{Description: "URN of the volume, which carries its uuid.", Computed: true},
			"status":         schema.StringAttribute{Description: "Lifecycle status reported by infra-gateway.", Computed: true},
			"last_error":     schema.StringAttribute{Description: "Last error reported by infra-gateway, empty when there is none.", Computed: true},
			"attached_to_vm": schema.StringAttribute{Description: "Instance id of the virtual machine the volume is attached to, empty when it is detached. infra-gateway reports the machine's uuid here, not its name.", Computed: true},
			"tags": schema.MapAttribute{
				Description: "Tags applied to the volume. Tags are updated in place.",
				Optional:    true, ElementType: types.StringType,
			},
			"labels":      schema.MapAttribute{Description: "Labels infra-gateway reports for the volume.", Computed: true, ElementType: types.StringType},
			"annotations": schema.MapAttribute{Description: "Annotations infra-gateway reports for the volume.", Computed: true, ElementType: types.StringType},
		},
	}
}

func (r *Resource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	runtime, ok := req.ProviderData.(*client.Runtime)
	if !ok {
		resp.Diagnostics.AddError("Unexpected resource configuration", fmt.Sprintf("Expected *client.Runtime, got %T.", req.ProviderData))
		return
	}
	r.client = runtime
}

// ModifyPlan rejects shrinking and marks changed computed fields unknown. Otherwise a
// resize would produce values inconsistent with the plan.
func (r *Resource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Only an update can shrink a volume: a create has no prior size and a
	// destroy has no plan.
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var state, plan Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Size.IsUnknown() && !plan.Size.IsNull() && !state.Size.IsNull() {
		planned, err := client.ParseQuantity(plan.Size.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("size"), "Invalid volume size", err.Error())
			return
		}
		current, err := client.ParseQuantity(state.Size.ValueString())
		if err == nil && planned < current {
			resp.Diagnostics.AddAttributeError(path.Root("size"), "Volume cannot be shrunk", fmt.Sprintf(
				"Volume %q is %s (%d bytes) and the configuration asks for %s (%d bytes). infra-gateway only grows a volume, so shrinking requires replacing it.",
				state.Name.ValueString(), state.Size.ValueString(), current, plan.Size.ValueString(), planned))
			return
		}
	}

	if req.Plan.Raw.Equal(req.State.Raw) {
		return
	}
	for _, attribute := range []struct {
		name  path.Path
		value any
	}{
		{path.Root("size_bytes"), types.Int64Unknown()},
		{path.Root("status"), types.StringUnknown()},
		{path.Root("last_error"), types.StringUnknown()},
		{path.Root("attached_to_vm"), types.StringUnknown()},
		{path.Root("labels"), types.MapUnknown(types.StringType)},
		{path.Root("annotations"), types.MapUnknown(types.StringType)},
	} {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, attribute.name, attribute.value)...)
	}
}

func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateVolume(ctx, client.CreateVolumeRequest{
		Name: plan.Name.ValueString(), Size: plan.Size.ValueString(),
		StorageClass: plan.StorageClass.ValueString(), AccessMode: plan.AccessMode.ValueString(),
		Tags: common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume", err.Error())
		return
	}
	setFromVolume(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetVolume(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume", err.Error())
		return
	}
	setFromVolume(ctx, &state, current, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	planned, err := client.ParseQuantity(plan.Size.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("size"), "Invalid volume size", err.Error())
		return
	}

	// The configured spelling and the stored one can differ without the volume
	// changing size, so the byte count decides whether a resize is needed.
	if planned != state.SizeBytes.ValueInt64() {
		if _, err := r.client.ResizeVolume(ctx, name, plan.Size.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error resizing volume", err.Error())
			return
		}
	}

	if !plan.Tags.Equal(state.Tags) {
		current := common.TagsToClient(ctx, state.Tags, &resp.Diagnostics)
		desired := common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		if _, err := r.client.UpdateVolumeTags(ctx, name, current, desired); err != nil {
			resp.Diagnostics.AddError("Error updating volume tags", err.Error())
			return
		}
	}

	// Read the settled result after either asynchronous update.
	current, err := r.client.GetVolume(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume", err.Error())
		return
	}
	setFromVolume(ctx, &plan, current, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteVolume(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting volume", err.Error())
	}
}

func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// setFromVolume writes the authoritative read into model. model.Size holds the
// configured or previously stored spelling on entry and is kept when it is
// numerically equal to what the gateway reports.
func setFromVolume(ctx context.Context, model *Model, value client.Volume, diags *diag.Diagnostics) {
	actual, err := client.ParseQuantity(value.Size)
	if err != nil {
		diags.AddError("Unsupported volume size", fmt.Sprintf("infra-gateway reported size %q for volume %q: %s", value.Size, value.Name, err))
		return
	}

	// Preserve the configured spelling when byte counts match to avoid permanent diffs.
	if !model.Size.IsNull() && !model.Size.IsUnknown() {
		if prior, priorErr := client.ParseQuantity(model.Size.ValueString()); priorErr != nil || prior != actual {
			model.Size = types.StringValue(value.Size)
		}
	} else {
		model.Size = types.StringValue(value.Size)
	}
	model.SizeBytes = types.Int64Value(actual)

	model.Name = types.StringValue(value.Name)
	model.StorageClass = types.StringValue(value.StorageClass)
	model.AccessMode = types.StringValue(value.AccessMode)
	model.VolumeMode = types.StringValue(value.VolumeMode)
	model.URN = types.StringValue(value.URN)
	model.Status = types.StringValue(value.Status)
	model.LastError = types.StringValue(value.LastError)
	model.AttachedToVM = types.StringValue(value.AttachedToVM)
	model.Tags = common.TagsToTerraform(ctx, value.Tags, diags)
	model.Labels = stringMapToTerraform(ctx, value.Labels, diags)
	model.Annotations = stringMapToTerraform(ctx, value.Annotations, diags)
}

func stringMapToTerraform(ctx context.Context, values map[string]string, diags *diag.Diagnostics) types.Map {
	if len(values) == 0 {
		return types.MapNull(types.StringType)
	}
	result, conversionDiags := types.MapValueFrom(ctx, types.StringType, values)
	diags.Append(conversionDiags...)
	return result
}
