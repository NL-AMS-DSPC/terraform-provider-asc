// Package vmgroup implements the asc_vm_group resource.
package vmgroup

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
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
)

type resourceClient interface {
	CreateVMGroup(context.Context, client.CreateVMGroupRequest) (client.VMGroup, error)
	GetVMGroup(context.Context, string) (client.VMGroup, error)
	DeleteVMGroup(context.Context, string) error
}

type Resource struct {
	client resourceClient
}

type Model struct {
	URN             types.String        `tfsdk:"urn"`
	Name            types.String        `tfsdk:"name"`
	SKUID           types.String        `tfsdk:"sku_id"`
	SKU             types.Object        `tfsdk:"sku"`
	VPCID           types.String        `tfsdk:"vpc_id"`
	Image           types.String        `tfsdk:"image"`
	Autoscaling     *AutoscalingModel   `tfsdk:"autoscaling"`
	Status          types.String        `tfsdk:"status"`
	LastError       types.String        `tfsdk:"last_error"`
	Tags            types.Map           `tfsdk:"tags"`
	AttachedVolumes types.List          `tfsdk:"attached_volumes"`
	Customization   *CustomizationModel `tfsdk:"customization"`
}

type AutoscalingModel struct {
	MinReplicas types.Int64 `tfsdk:"min_replicas"`
	MaxReplicas types.Int64 `tfsdk:"max_replicas"`
	Cron        *CronModel  `tfsdk:"cron"`
}

type CronModel struct {
	Timezone        types.String `tfsdk:"timezone"`
	Start           types.String `tfsdk:"start"`
	End             types.String `tfsdk:"end"`
	DesiredReplicas types.Int64  `tfsdk:"desired_replicas"`
}

type CustomizationModel struct {
	CloudInit *CloudInitModel `tfsdk:"cloud_init"`
	Ignition  *IgnitionModel  `tfsdk:"ignition"`
}

type CloudInitModel struct {
	UserData types.String `tfsdk:"user_data"`
	MetaData types.String `tfsdk:"meta_data"`
}

type IgnitionModel struct {
	Format types.String `tfsdk:"format"`
	Config types.String `tfsdk:"config"`
}

type skuModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Family      types.String `tfsdk:"family"`
	Threads     types.Int64  `tfsdk:"threads"`
	Cores       types.Int64  `tfsdk:"cores"`
	MemoryInMB  types.Int64  `tfsdk:"memory_in_mb"`
	StorageInGB types.Int64  `tfsdk:"storage_in_gb"`
	StorageType types.String `tfsdk:"storage_type"`
	GPUCount    types.Int64  `tfsdk:"gpu_count"`
	GPUType     types.String `tfsdk:"gpu_type"`
}

var skuTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "family": types.StringType,
	"threads": types.Int64Type, "cores": types.Int64Type, "memory_in_mb": types.Int64Type,
	"storage_in_gb": types.Int64Type, "storage_type": types.StringType,
	"gpu_count": types.Int64Type, "gpu_type": types.StringType,
}

func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_group"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computedSKU := map[string]schema.Attribute{
		"id":            schema.StringAttribute{Description: "Identifier of the SKU.", Computed: true},
		"name":          schema.StringAttribute{Description: "Name of the SKU.", Computed: true},
		"family":        schema.StringAttribute{Description: "Instance family of the SKU.", Computed: true},
		"threads":       schema.Int64Attribute{Description: "Threads per core.", Computed: true},
		"cores":         schema.Int64Attribute{Description: "Physical cores.", Computed: true},
		"memory_in_mb":  schema.Int64Attribute{Description: "Memory in MB.", Computed: true},
		"storage_in_gb": schema.Int64Attribute{Description: "Storage in GB.", Computed: true},
		"storage_type":  schema.StringAttribute{Description: "Storage type backing the SKU.", Computed: true},
		"gpu_count":     schema.Int64Attribute{Description: "Number of GPUs, 0 when the SKU has none.", Computed: true},
		"gpu_type":      schema.StringAttribute{Description: "GPU model, empty when the SKU has none.", Computed: true},
	}

	resp.Schema = schema.Schema{
		Description: "Manages an ASC virtual machine group.",
		Attributes: map[string]schema.Attribute{
			"urn": schema.StringAttribute{Description: "Resource identifier of the group.", Computed: true},
			"name": schema.StringAttribute{
				Description:   "Name of the virtual machine group. Must be 1 to 50 characters: a lowercase letter first, then lowercase letters, digits or hyphens, ending with a letter or digit. Changing it replaces the group.",
				Required:      true,
				PlanModifiers: replaceString,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`), "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, and end with a letter or digit"),
					stringvalidator.LengthBetween(1, 50),
				},
			},
			"sku_id": schema.StringAttribute{
				Description: "Identifier of the SKU backing every member of the group; list them with the asc_skus data source. " +
					"Changing it replaces the group.",
				Required: true, PlanModifiers: replaceString,
			},
			"sku": schema.SingleNestedAttribute{Description: "SKU backing every member of the group.", Computed: true, Attributes: computedSKU},
			"vpc_id": schema.StringAttribute{
				Description:   "Identifier of the VPC the group belongs to, a UUID version 4. Changing it replaces the group.",
				Required:      true,
				PlanModifiers: replaceString,
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`),
					"must be a UUID version 4",
				)},
			},
			// The gateway never returns image, so it cannot be Computed.
			"image": schema.StringAttribute{
				Description: "Identifier of the operating system image, for example ubuntu-24.04-lts. " +
					"When omitted, the platform default is used. The gateway never returns it, so the configured value is kept in state. " +
					"Changing it replaces the group.",
				Optional: true, PlanModifiers: replaceString,
			},
			"autoscaling": schema.SingleNestedAttribute{
				Description:   "Scaling bounds of the group. Changing the policy replaces the group.",
				Required:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"min_replicas": schema.Int64Attribute{Description: "Lower replica bound, 1 to 100.", Required: true, Validators: []validator.Int64{int64validator.Between(1, 100)}},
					"max_replicas": schema.Int64Attribute{
						Description: "Upper replica bound, 1 to 100 and at least min_replicas.",
						Required:    true,
						Validators: []validator.Int64{
							int64validator.Between(1, 100),
							int64validator.AtLeastSumOf(path.MatchRelative().AtParent().AtName("min_replicas")),
						},
					},
					"cron": schema.SingleNestedAttribute{
						Description: "Cron window that overrides the replica count while it is open.",
						Optional:    true,
						Attributes: map[string]schema.Attribute{
							"timezone":         schema.StringAttribute{Description: "IANA timezone the window is expressed in.", Required: true},
							"start":            schema.StringAttribute{Description: "Cron expression opening the window.", Required: true},
							"end":              schema.StringAttribute{Description: "Cron expression closing the window.", Required: true},
							"desired_replicas": schema.Int64Attribute{Description: "Replica count held while the window is open, 0 to 100.", Required: true, Validators: []validator.Int64{int64validator.Between(0, 100)}},
						},
					},
				},
			},
			"status":     schema.StringAttribute{Description: "Current lifecycle status.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the group, empty when there is none.", Computed: true},
			// The gateway has no group tag-patch route; changes require replacement.
			"tags": schema.MapAttribute{
				Description: "Tags applied to the group. The gateway cannot patch group tags, so changing them replaces the group.",
				Optional:    true, ElementType: types.StringType,
				PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"attached_volumes": schema.ListAttribute{
				Description: "Names of the volumes attached to the group, sorted; the gateway does not report them in a stable order.",
				Computed:    true, ElementType: types.StringType,
			},
			"customization": schema.SingleNestedAttribute{
				Description: "First-boot customization of the group, as cloud-init or Ignition data. Sensitive: " +
					"The gateway never returns it, so the configured value is kept in state. Changing it replaces the group.",
				Optional: true, Sensitive: true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"cloud_init": schema.SingleNestedAttribute{
						Description: "Cloud-init customization.",
						Optional:    true, Sensitive: true,
						Attributes: map[string]schema.Attribute{
							"user_data": schema.StringAttribute{Description: "Cloud-init user data.", Optional: true, Sensitive: true},
							"meta_data": schema.StringAttribute{Description: "Cloud-init meta data.", Optional: true, Sensitive: true},
						},
					},
					"ignition": schema.SingleNestedAttribute{
						Description: "Ignition customization.",
						Optional:    true, Sensitive: true,
						Attributes: map[string]schema.Attribute{
							"format": schema.StringAttribute{Description: "Format of the Ignition config.", Required: true},
							"config": schema.StringAttribute{Description: "Ignition config content.", Required: true, Sensitive: true},
						},
					},
				},
			},
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

func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateVMGroup(ctx, client.CreateVMGroupRequest{
		Name: plan.Name.ValueString(), SKUID: plan.SKUID.ValueString(), VPCID: plan.VPCID.ValueString(),
		Image: plan.Image.ValueString(), AutoscalingPolicy: autoscalingToClient(plan.Autoscaling),
		Tags: common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics), Customization: customizationToClient(plan.Customization),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating virtual machine group", err.Error())
		return
	}
	setFromVMGroup(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetVMGroup(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine group", err.Error())
		return
	}
	setFromVMGroup(ctx, &state, current, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable because all configurable fields require replacement.
func (r *Resource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteVMGroup(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting virtual machine group", err.Error())
	}
}

// ImportState uses the group name. The API cannot recover VPC ID, image or customization;
// configuring these after import can force replacement.
func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func autoscalingToClient(value *AutoscalingModel) client.AutoscalingPolicy {
	if value == nil {
		return client.AutoscalingPolicy{}
	}
	policy := client.AutoscalingPolicy{MinReplicas: value.MinReplicas.ValueInt64(), MaxReplicas: value.MaxReplicas.ValueInt64()}
	if value.Cron != nil {
		policy.Cron = &client.CronRule{
			Timezone: value.Cron.Timezone.ValueString(), Start: value.Cron.Start.ValueString(),
			End: value.Cron.End.ValueString(), DesiredReplicas: value.Cron.DesiredReplicas.ValueInt64(),
		}
	}
	return policy
}

func autoscalingToTerraform(value client.AutoscalingPolicy) *AutoscalingModel {
	result := &AutoscalingModel{MinReplicas: types.Int64Value(value.MinReplicas), MaxReplicas: types.Int64Value(value.MaxReplicas)}
	if value.Cron != nil {
		result.Cron = &CronModel{
			Timezone: types.StringValue(value.Cron.Timezone), Start: types.StringValue(value.Cron.Start),
			End: types.StringValue(value.Cron.End), DesiredReplicas: types.Int64Value(value.Cron.DesiredReplicas),
		}
	}
	return result
}

func customizationToClient(value *CustomizationModel) *client.Customization {
	if value == nil {
		return nil
	}
	result := &client.Customization{}
	if value.CloudInit != nil {
		result.CloudInit = &client.CloudInitCustomization{UserData: value.CloudInit.UserData.ValueString(), MetaData: value.CloudInit.MetaData.ValueString()}
	}
	if value.Ignition != nil {
		result.Ignition = &client.IgnitionCustomization{Format: value.Ignition.Format.ValueString(), Config: value.Ignition.Config.ValueString()}
	}
	return result
}

// setFromVMGroup preserves write-only VPC ID, image and customization.
func setFromVMGroup(ctx context.Context, model *Model, value client.VMGroup, diags *diag.Diagnostics) {
	model.URN = types.StringValue(value.URN)
	model.Name = types.StringValue(value.Name)
	model.SKUID = types.StringValue(value.SKU.ID)
	model.Status = types.StringValue(value.Status)
	model.LastError = types.StringValue(value.LastError)
	model.Tags = common.TagsToTerraform(ctx, value.Tags, diags)
	model.Autoscaling = autoscalingToTerraform(value.AutoscalingPolicy)

	sku, skuDiags := types.ObjectValueFrom(ctx, skuTypes, skuModel{
		ID: types.StringValue(value.SKU.ID), Name: types.StringValue(value.SKU.Name), Family: types.StringValue(value.SKU.Family),
		Threads: types.Int64Value(int64(value.SKU.Threads)), Cores: types.Int64Value(int64(value.SKU.Cores)),
		MemoryInMB: types.Int64Value(int64(value.SKU.MemoryInMB)), StorageInGB: types.Int64Value(int64(value.SKU.StorageInGB)),
		StorageType: types.StringValue(value.SKU.StorageType), GPUCount: types.Int64Value(int64(value.SKU.GPUCount)),
		GPUType: types.StringValue(value.SKU.GPUType),
	})
	diags.Append(skuDiags...)
	model.SKU = sku

	volumes, volumeDiags := types.ListValueFrom(ctx, types.StringType, value.AttachedVolumes)
	diags.Append(volumeDiags...)
	model.AttachedVolumes = volumes
}
