package subnet

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

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
)

type resourceClient interface {
	CreateSubnet(context.Context, string, client.CreateSubnetRequest) (client.Subnet, error)
	GetSubnet(context.Context, string, string) (client.Subnet, error)
	UpdateSubnetTags(context.Context, string, string, []client.Tag, []client.Tag) (client.Subnet, error)
	DeleteSubnet(context.Context, string, string) error
}

type Resource struct {
	client resourceClient
}

type Model struct {
	ID        types.String `tfsdk:"id"`
	URN       types.String `tfsdk:"urn"`
	Name      types.String `tfsdk:"name"`
	VPCName   types.String `tfsdk:"vpc_name"`
	VPCID     types.String `tfsdk:"vpc_id"`
	CIDR      types.String `tfsdk:"cidr"`
	Type      types.String `tfsdk:"type"`
	Status    types.String `tfsdk:"status"`
	LastError types.String `tfsdk:"last_error"`
	Tags      types.Map    `tfsdk:"tags"`
}

func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Manages an ASC subnet.",
		Attributes: map[string]schema.Attribute{
			"id":       schema.StringAttribute{Computed: true},
			"urn":      schema.StringAttribute{Computed: true},
			"name":     schema.StringAttribute{Required: true, PlanModifiers: replaceString},
			"vpc_name": schema.StringAttribute{Required: true, PlanModifiers: replaceString},
			"vpc_id": schema.StringAttribute{
				Required:      true,
				PlanModifiers: replaceString,
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`),
					"must be a UUID version 4",
				)},
			},
			"cidr": schema.StringAttribute{Required: true, PlanModifiers: replaceString},
			"type": schema.StringAttribute{
				Description:        "Deprecated: ASC no longer has subnet types. The value is ignored and never sent to ASC.",
				DeprecationMessage: common.SubnetTypeDeprecation,
				Optional:           true, Computed: true,
				PlanModifiers: []planmodifier.String{common.IgnoredString()},
			},
			"status":     schema.StringAttribute{Computed: true},
			"last_error": schema.StringAttribute{Computed: true},
			"tags": schema.MapAttribute{
				Description: "Tags applied to the subnet. Changing tags updates the subnet in place.",
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

	created, err := r.client.CreateSubnet(ctx, plan.VPCName.ValueString(), client.CreateSubnetRequest{
		Name: plan.Name.ValueString(), CIDR: plan.CIDR.ValueString(), VPCID: plan.VPCID.ValueString(),
		Tags: common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating subnet", err.Error())
		return
	}
	setFromSubnet(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetSubnet(ctx, state.VPCName.ValueString(), state.Name.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading subnet", err.Error())
		return
	}
	setFromSubnet(ctx, &state, current, &resp.Diagnostics)
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

	updated, err := r.client.UpdateSubnetTags(ctx, state.VPCName.ValueString(), state.Name.ValueString(), current, desired)
	if err != nil {
		resp.Diagnostics.AddError("Error updating subnet tags", err.Error())
		return
	}
	setFromSubnet(ctx, &plan, updated, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSubnet(ctx, state.VPCName.ValueString(), state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting subnet", err.Error())
	}
}

func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	separator := strings.Index(req.ID, ":")
	if separator <= 0 || separator == len(req.ID)-1 {
		resp.Diagnostics.AddError("Invalid subnet import identifier", "Use vpc-name:subnet-name.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vpc_name"), req.ID[:separator])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID[separator+1:])...)
}

func setFromSubnet(ctx context.Context, model *Model, value client.Subnet, diags *diag.Diagnostics) {
	model.ID = types.StringValue(value.ID)
	model.URN = types.StringValue(value.URN)
	model.Name = types.StringValue(value.Name)
	model.VPCID = types.StringValue(value.VPCID)
	model.CIDR = types.StringValue(value.CIDR)
	// ASC no longer reports a type; keep the configured or stored one.
	if model.Type.IsUnknown() {
		model.Type = types.StringNull()
	}
	model.Status = types.StringValue(value.Status)
	model.LastError = types.StringValue(value.LastError)
	model.Tags = common.TagsToTerraform(ctx, value.Tags, diags)
}
