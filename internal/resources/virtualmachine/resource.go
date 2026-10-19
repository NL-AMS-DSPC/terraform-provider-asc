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
	UpdateVMTags(context.Context, string, []client.Tag, []client.Tag) (client.VM, error)
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
	// Preserve omitted computed values before checking replacement, avoiding replacement
	// during unrelated updates.
	keepOrReplaceString := []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}
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
	computedOS := map[string]schema.Attribute{
		"id":           schema.StringAttribute{Description: "Identifier of the operating system image.", Computed: true},
		"family":       schema.StringAttribute{Description: "Operating system family.", Computed: true},
		"distribution": schema.StringAttribute{Description: "Operating system distribution.", Computed: true},
		"release":      schema.StringAttribute{Description: "Operating system release.", Computed: true},
		"display_name": schema.StringAttribute{Description: "Human readable name of the image.", Computed: true},
	}

	resp.Schema = schema.Schema{
		Description: "Manages an ASC virtual machine.",
		Attributes: map[string]schema.Attribute{
			"urn": schema.StringAttribute{Description: "URN of the virtual machine.", Computed: true},
			"name": schema.StringAttribute{
				Description:   "Name of the virtual machine. Must be 1 to 50 characters: a lowercase letter first, then lowercase letters, digits or hyphens, ending with a letter or digit. Changing it replaces the machine.",
				Required:      true,
				PlanModifiers: replaceString,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`), "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, and end with a letter or digit"),
					stringvalidator.LengthBetween(1, 50),
				},
			},
			"sku_id": schema.StringAttribute{
				Description: "Identifier of the SKU to run on; list them with the asc_skus data source. " +
					"Changing it replaces the machine.",
				Required: true, PlanModifiers: replaceString,
			},
			"sku": schema.SingleNestedAttribute{Description: "SKU the virtual machine runs on.", Computed: true, Attributes: computedSKU},
			"vpc_id": schema.StringAttribute{
				Description: "Identifier of the VPC the virtual machine is attached to. Changing it replaces the machine.",
				Required:    true, PlanModifiers: replaceString,
			},
			"image": schema.StringAttribute{
				Description: "Identifier of the operating system image, for example vm-images/ubuntu:24.04, which is also the default. " +
					"Changing the configured value replaces the machine.",
				Optional: true, Computed: true, PlanModifiers: keepOrReplaceString,
			},
			"status":     schema.StringAttribute{Description: "Current lifecycle status.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the virtual machine, empty when healthy.", Computed: true},
			"tags": schema.MapAttribute{
				Description: "Tags applied to the virtual machine. Changing tags updates the machine in place.",
				Optional:    true, ElementType: types.StringType,
			},
			"attached_volumes": schema.ListAttribute{
				Description: "Names of the volumes attached to the machine, sorted.",
				Computed:    true, ElementType: types.StringType,
			},
			"os": schema.SingleNestedAttribute{
				Description: "Operating system image the virtual machine boots.", Computed: true, Attributes: computedOS,
			},
			"customization": schema.SingleNestedAttribute{
				Description: "First-boot customization of the virtual machine, as cloud-init or Ignition data. Sensitive, " +
					"and cannot be recovered by import. Changing it replaces the virtual machine.",
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
	created, err := r.client.CreateVM(ctx, client.CreateVMRequest{
		Name: plan.Name.ValueString(), SKUID: plan.SKUID.ValueString(), VPCID: plan.VPCID.ValueString(),
		Image: plan.Image.ValueString(), Tags: common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics),
		Customization: customizationToClient(plan.Customization),
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

// Update patches tags. SKU changes require replacement because the gateway never clears
// its VM resize latch after the first resize.
func (r *Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current := common.TagsToClient(ctx, state.Tags, &resp.Diagnostics)
	desired := common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateVMTags(ctx, state.Name.ValueString(), current, desired)
	if err != nil {
		resp.Diagnostics.AddError("Error updating virtual machine tags", err.Error())
		return
	}
	setFromVM(ctx, &plan, updated, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

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
