package virtualmachine

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
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
	CreateVM(context.Context, client.CreateVMRequest) (client.VM, error)
	GetVM(context.Context, string) (client.VM, error)
	DeleteVM(context.Context, string) error
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
	Status          types.String        `tfsdk:"status"`
	LastError       types.String        `tfsdk:"last_error"`
	Tags            types.Map           `tfsdk:"tags"`
	AttachedVolumes types.List          `tfsdk:"attached_volumes"`
	OS              types.Object        `tfsdk:"os"`
	Customization   *CustomizationModel `tfsdk:"customization"`
	EnableLogging   types.Bool          `tfsdk:"enable_logging"`
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

type osModel struct {
	ID           types.String `tfsdk:"id"`
	Family       types.String `tfsdk:"family"`
	Distribution types.String `tfsdk:"distribution"`
	Release      types.String `tfsdk:"release"`
	DisplayName  types.String `tfsdk:"display_name"`
}

var skuTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "family": types.StringType,
	"threads": types.Int64Type, "cores": types.Int64Type, "memory_in_mb": types.Int64Type,
	"storage_in_gb": types.Int64Type, "storage_type": types.StringType,
	"gpu_count": types.Int64Type, "gpu_type": types.StringType,
}

var osTypes = map[string]attr.Type{
	"id": types.StringType, "family": types.StringType, "distribution": types.StringType,
	"release": types.StringType, "display_name": types.StringType,
}

func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computedSKU := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true},
		"family": schema.StringAttribute{Computed: true}, "threads": schema.Int64Attribute{Computed: true},
		"cores": schema.Int64Attribute{Computed: true}, "memory_in_mb": schema.Int64Attribute{Computed: true},
		"storage_in_gb": schema.Int64Attribute{Computed: true}, "storage_type": schema.StringAttribute{Computed: true},
		"gpu_count": schema.Int64Attribute{Computed: true}, "gpu_type": schema.StringAttribute{Computed: true},
	}
	computedOS := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true}, "family": schema.StringAttribute{Computed: true},
		"distribution": schema.StringAttribute{Computed: true}, "release": schema.StringAttribute{Computed: true},
		"display_name": schema.StringAttribute{Computed: true},
	}

	resp.Schema = schema.Schema{
		Description: "Manages a virtual machine through ASC infra-gateway.",
		Attributes: map[string]schema.Attribute{
			"urn": schema.StringAttribute{Computed: true},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: replaceString,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`), "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, and end with a letter or digit"),
					stringvalidator.LengthBetween(1, 50),
				},
			},
			"sku_id": schema.StringAttribute{Required: true, PlanModifiers: replaceString},
			"sku":    schema.SingleNestedAttribute{Computed: true, Attributes: computedSKU},
			"vpc_id": schema.StringAttribute{Required: true, PlanModifiers: replaceString},
			"image": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: replaceString,
			},
			"status":     schema.StringAttribute{Computed: true},
			"last_error": schema.StringAttribute{Computed: true},
			"tags": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"attached_volumes": schema.ListAttribute{Computed: true, ElementType: types.StringType},
			"os":               schema.SingleNestedAttribute{Computed: true, Attributes: computedOS},
			"customization": schema.SingleNestedAttribute{
				Optional: true, Sensitive: true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"cloud_init": schema.SingleNestedAttribute{
						Optional: true, Sensitive: true,
						Attributes: map[string]schema.Attribute{
							"user_data": schema.StringAttribute{Optional: true, Sensitive: true},
							"meta_data": schema.StringAttribute{Optional: true, Sensitive: true},
						},
					},
					"ignition": schema.SingleNestedAttribute{
						Optional: true, Sensitive: true,
						Attributes: map[string]schema.Attribute{
							"format": schema.StringAttribute{Required: true},
							"config": schema.StringAttribute{Required: true, Sensitive: true},
						},
					},
				},
			},
			"enable_logging": schema.BoolAttribute{
				Optional: true, Computed: true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
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
	created, err := r.client.CreateVM(ctx, client.CreateVMRequest{
		Name: plan.Name.ValueString(), SKUID: plan.SKUID.ValueString(), VPCID: plan.VPCID.ValueString(),
		Image: plan.Image.ValueString(), Tags: common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics),
		Customization: customizationToClient(plan.Customization), EnableLogging: plan.EnableLogging.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating virtual machine", err.Error())
		return
	}
	setFromVM(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetVM(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
		return
	}
	setFromVM(ctx, &state, current, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *Resource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteVM(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting virtual machine", err.Error())
	}
}

func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
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

func setFromVM(ctx context.Context, model *Model, value client.VM, diags *diag.Diagnostics) {
	model.URN = types.StringValue(value.URN)
	model.Name = types.StringValue(value.Name)
	model.SKUID = types.StringValue(value.SKU.ID)
	model.VPCID = types.StringValue(value.VPCID)
	model.Image = types.StringValue(value.OS.ID)
	model.Status = types.StringValue(value.Status)
	model.LastError = types.StringValue(value.LastError)
	model.Tags = common.TagsToTerraform(ctx, value.Tags, diags)
	model.EnableLogging = types.BoolValue(value.EnableLogging)

	sku, skuDiags := types.ObjectValueFrom(ctx, skuTypes, skuModel{
		ID: types.StringValue(value.SKU.ID), Name: types.StringValue(value.SKU.Name), Family: types.StringValue(value.SKU.Family),
		Threads: types.Int64Value(int64(value.SKU.Threads)), Cores: types.Int64Value(int64(value.SKU.Cores)),
		MemoryInMB: types.Int64Value(int64(value.SKU.MemoryInMB)), StorageInGB: types.Int64Value(int64(value.SKU.StorageInGB)),
		StorageType: types.StringValue(value.SKU.StorageType), GPUCount: types.Int64Value(int64(value.SKU.GPUCount)),
		GPUType: types.StringValue(value.SKU.GPUType),
	})
	diags.Append(skuDiags...)
	model.SKU = sku

	osValue, osDiags := types.ObjectValueFrom(ctx, osTypes, osModel{
		ID: types.StringValue(value.OS.ID), Family: types.StringValue(value.OS.Family),
		Distribution: types.StringValue(value.OS.Distribution), Release: types.StringValue(value.OS.Release),
		DisplayName: types.StringValue(value.OS.DisplayName),
	})
	diags.Append(osDiags...)
	model.OS = osValue

	volumes, volumeDiags := types.ListValueFrom(ctx, types.StringType, value.AttachedVolumes)
	diags.Append(volumeDiags...)
	model.AttachedVolumes = volumes
}
