package vpc

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
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
	CreateVPC(context.Context, client.CreateVPCRequest) (client.VPC, error)
	GetVPC(context.Context, string) (client.VPC, error)
	UpdateVPCTags(context.Context, string, []client.Tag, []client.Tag) (client.VPC, error)
	DeleteVPC(context.Context, string) error
}

type Resource struct {
	client resourceClient
}

type Model struct {
	ID        types.String `tfsdk:"id"`
	URN       types.String `tfsdk:"urn"`
	Name      types.String `tfsdk:"name"`
	CIDR      types.String `tfsdk:"cidr"`
	Status    types.String `tfsdk:"status"`
	LastError types.String `tfsdk:"last_error"`
	Subnets   types.List   `tfsdk:"subnets"`
	Tags      types.Map    `tfsdk:"tags"`
}

type inlineSubnetModel struct {
	ID        types.String `tfsdk:"id"`
	URN       types.String `tfsdk:"urn"`
	Name      types.String `tfsdk:"name"`
	VPCID     types.String `tfsdk:"vpc_id"`
	CIDR      types.String `tfsdk:"cidr"`
	Status    types.String `tfsdk:"status"`
	LastError types.String `tfsdk:"last_error"`
	Tags      types.Map    `tfsdk:"tags"`
}

var inlineSubnetTypes = map[string]attr.Type{
	"id":         types.StringType,
	"urn":        types.StringType,
	"name":       types.StringType,
	"vpc_id":     types.StringType,
	"cidr":       types.StringType,
	"status":     types.StringType,
	"last_error": types.StringType,
	"tags":       types.MapType{ElemType: types.StringType},
}

func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an ASC VPC.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true},
			"urn":  schema.StringAttribute{Computed: true},
			"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"cidr": schema.StringAttribute{
				Description:   "IPv4 CIDR block of the VPC, such as 10.0.0.0/16. Every inline subnet must lie within it. Changing it replaces the VPC.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"status":     schema.StringAttribute{Computed: true},
			"last_error": schema.StringAttribute{Computed: true},
			"subnets": schema.ListNestedAttribute{
				Required: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":         schema.StringAttribute{Computed: true},
					"urn":        schema.StringAttribute{Computed: true},
					"name":       schema.StringAttribute{Required: true},
					"vpc_id":     schema.StringAttribute{Computed: true},
					"cidr":       schema.StringAttribute{Required: true},
					"status":     schema.StringAttribute{Computed: true},
					"last_error": schema.StringAttribute{Computed: true},
					"tags":       schema.MapAttribute{Optional: true, ElementType: types.StringType},
				}},
				PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplaceIf(
					inlineSubnetsRequireReplacement,
					"Changing an inline subnet replaces the VPC.",
					"Changing an inline subnet replaces the VPC.",
				)},
				Validators: []validator.List{listvalidator.SizeAtLeast(1)},
			},
			"tags": schema.MapAttribute{
				Description: "Tags applied to the VPC. Changing these tags updates the VPC in place. " +
					"Changing tags on an inline subnet replaces the VPC.",
				Optional:    true,
				ElementType: types.StringType,
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

	request := client.CreateVPCRequest{
		Name:    plan.Name.ValueString(),
		CIDR:    plan.CIDR.ValueString(),
		Tags:    common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics),
		Subnets: inlineSubnetsToClient(ctx, plan.Subnets, &resp.Diagnostics),
	}
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateVPC(ctx, request)
	if err != nil {
		resp.Diagnostics.AddError("Error creating VPC", err.Error())
		return
	}
	setFromVPC(ctx, &plan, created, false, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetVPC(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading VPC", err.Error())
		return
	}
	setFromVPC(ctx, &state, current, true, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update patches tags; other configurable fields require replacement.
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

	updated, err := r.client.UpdateVPCTags(ctx, state.Name.ValueString(), current, desired)
	if err != nil {
		resp.Diagnostics.AddError("Error updating VPC tags", err.Error())
		return
	}
	setFromVPC(ctx, &plan, updated, true, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteVPC(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting VPC", err.Error())
	}
}

func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// inlineSubnetsRequireReplacement compares configurable fields only. Computed fields can
// be unknown during unrelated tag updates and must not trigger replacement.
func inlineSubnetsRequireReplacement(ctx context.Context, req planmodifier.ListRequest, resp *listplanmodifier.RequiresReplaceIfFuncResponse) {
	if req.ConfigValue.IsUnknown() {
		// Unknown subnet configuration cannot be proven unchanged.
		resp.RequiresReplace = true
		return
	}

	configured := configuredInlineSubnets(ctx, req.ConfigValue, &resp.Diagnostics)
	prior := configuredInlineSubnets(ctx, req.StateValue, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.RequiresReplace = !slices.Equal(configured, prior)
}

// configuredInlineSubnets projects the list onto the fields a configuration can
// set, in order, so two lists can be compared without the computed attributes
// getting in the way.
func configuredInlineSubnets(ctx context.Context, value types.List, diags *diag.Diagnostics) []string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	var models []inlineSubnetModel
	diags.Append(value.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return nil
	}
	result := make([]string, 0, len(models))
	for _, model := range models {
		result = append(result, fmt.Sprintf("%s|%s|%s", model.Name, model.CIDR, model.Tags))
	}
	return result
}

func inlineSubnetsToClient(ctx context.Context, value types.List, diags *diag.Diagnostics) []client.CreateSubnetRequest {
	var models []inlineSubnetModel
	diags.Append(value.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return nil
	}
	result := make([]client.CreateSubnetRequest, 0, len(models))
	for _, model := range models {
		result = append(result, client.CreateSubnetRequest{
			Name: model.Name.ValueString(), CIDR: model.CIDR.ValueString(),
			Tags: common.TagsToClient(ctx, model.Tags, diags),
		})
	}
	return result
}

func setFromVPC(ctx context.Context, model *Model, value client.VPC, reconcile bool, diags *diag.Diagnostics) {
	model.ID = types.StringValue(value.ID)
	model.URN = types.StringValue(value.URN)
	model.Name = types.StringValue(value.Name)
	model.CIDR = types.StringValue(value.CIDR)
	model.Status = types.StringValue(value.Status)
	model.LastError = types.StringValue(value.LastError)
	model.Tags = common.TagsToTerraform(ctx, value.Tags, diags)
	if reconcile {
		model.Subnets = reconcileInlineSubnets(ctx, model.Subnets, value.Subnets, diags)
	} else {
		model.Subnets = inlineSubnetsToTerraform(ctx, value.Subnets, diags)
	}
}

func reconcileInlineSubnets(ctx context.Context, prior types.List, actual []client.Subnet, diags *diag.Diagnostics) types.List {
	if prior.IsNull() || prior.IsUnknown() {
		return inlineSubnetsToTerraform(ctx, actual, diags)
	}
	var owned []inlineSubnetModel
	diags.Append(prior.ElementsAs(ctx, &owned, false)...)
	if diags.HasError() {
		return prior
	}
	byName := make(map[string]client.Subnet, len(actual))
	for _, subnet := range actual {
		byName[subnet.Name] = subnet
	}
	selected := make([]client.Subnet, 0, len(owned))
	for _, subnet := range owned {
		if current, ok := byName[subnet.Name.ValueString()]; ok {
			selected = append(selected, current)
		}
	}
	return inlineSubnetsToTerraform(ctx, selected, diags)
}

func inlineSubnetsToTerraform(ctx context.Context, values []client.Subnet, diags *diag.Diagnostics) types.List {
	models := make([]inlineSubnetModel, 0, len(values))
	for _, subnet := range values {
		models = append(models, inlineSubnetModel{
			ID: types.StringValue(subnet.ID), URN: types.StringValue(subnet.URN), Name: types.StringValue(subnet.Name),
			VPCID: types.StringValue(subnet.VPCID), CIDR: types.StringValue(subnet.CIDR),
			Status: types.StringValue(subnet.Status), LastError: types.StringValue(subnet.LastError),
			Tags: common.TagsToTerraform(ctx, subnet.Tags, diags),
		})
	}
	result, conversionDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: inlineSubnetTypes}, models)
	diags.Append(conversionDiags...)
	return result
}
