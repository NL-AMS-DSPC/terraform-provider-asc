// Package vnic implements the asc_vnic resource.
package vnic

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
)

type resourceClient interface {
	CreateVNIC(context.Context, client.CreateVNICRequest) (client.VNIC, error)
	GetVNIC(context.Context, string) (client.VNIC, error)
	RenameVNIC(context.Context, string, string) (client.VNIC, error)
	UpdateVNICTags(context.Context, string, []client.Tag, []client.Tag) (client.VNIC, error)
	DeleteVNIC(context.Context, string) error
}

type Resource struct {
	client resourceClient
}

// Model identifies the VNIC by the UUID that ends its URN.
type Model struct {
	ID        types.String `tfsdk:"id"`
	URN       types.String `tfsdk:"urn"`
	Name      types.String `tfsdk:"name"`
	SubnetURN types.String `tfsdk:"subnet_urn"`
	VMURN     types.String `tfsdk:"vm_urn"`
	PrivateIP types.String `tfsdk:"private_ip"`
	PublicIP  types.String `tfsdk:"public_ip"`
	MAC       types.String `tfsdk:"mac"`
	Status    types.String `tfsdk:"status"`
	LastError types.String `tfsdk:"last_error"`
	Tags      types.Map    `tfsdk:"tags"`
}

func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vnic"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}

	resp.Schema = schema.Schema{
		Description: "Manages an ASC virtual network interface (VNIC). Attach it to a virtual machine by listing its urn in " +
			"the machine's vnics.",
		Attributes: map[string]schema.Attribute{
			"id":  schema.StringAttribute{Description: "UUID of the VNIC, the last segment of its URN.", Computed: true, PlanModifiers: keepString},
			"urn": schema.StringAttribute{Description: "URN of the VNIC.", Computed: true, PlanModifiers: keepString},
			"subnet_urn": schema.StringAttribute{
				Description:   "URN of the subnet the VNIC takes its private IP from. Changing it replaces the VNIC.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Description: "Name of the VNIC, 1 to 50 characters. Changing it renames the VNIC in place.",
				Optional:    true, Computed: true, PlanModifiers: keepString,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 50)},
			},
			"vm_urn":     schema.StringAttribute{Description: "URN of the virtual machine the VNIC is attached to, empty when detached.", Computed: true},
			"private_ip": schema.StringAttribute{Description: "Private IP address of the VNIC in its subnet.", Computed: true, PlanModifiers: keepString},
			"public_ip":  schema.StringAttribute{Description: "Public IP address of the VNIC, empty when it has none.", Computed: true},
			"mac":        schema.StringAttribute{Description: "MAC address of the VNIC.", Computed: true, PlanModifiers: keepString},
			"status":     schema.StringAttribute{Description: "Current lifecycle status, for example detached or attached.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the VNIC, empty when healthy.", Computed: true},
			"tags": schema.MapAttribute{
				Description: "Tags applied to the VNIC. Changing tags updates the VNIC in place.",
				Optional:    true, ElementType: types.StringType,
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

	created, err := r.client.CreateVNIC(ctx, client.CreateVNICRequest{
		Name: plan.Name.ValueString(), SubnetURN: plan.SubnetURN.ValueString(),
		Tags: common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating VNIC", err.Error())
		return
	}
	setFromVNIC(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetVNIC(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading VNIC", err.Error())
		return
	}
	setFromVNIC(ctx, &state, current, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update renames the VNIC and patches its tags; the subnet cannot change in place.
func (r *Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	if !plan.Name.IsUnknown() && !plan.Name.Equal(state.Name) {
		if _, err := r.client.RenameVNIC(ctx, id, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error renaming VNIC", err.Error())
			return
		}
	}

	current := common.TagsToClient(ctx, state.Tags, &resp.Diagnostics)
	desired := common.TagsToClient(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.client.UpdateVNICTags(ctx, id, current, desired)
	if err != nil {
		resp.Diagnostics.AddError("Error updating VNIC tags", err.Error())
		return
	}
	setFromVNIC(ctx, &plan, updated, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteVNIC(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting VNIC", err.Error())
	}
}

func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func setFromVNIC(ctx context.Context, model *Model, value client.VNIC, diags *diag.Diagnostics) {
	model.ID = types.StringValue(value.ID)
	model.URN = types.StringValue(value.URN)
	model.Name = types.StringValue(value.Name)
	model.SubnetURN = types.StringValue(value.SubnetURN)
	model.VMURN = types.StringValue(value.VMURN)
	model.PrivateIP = types.StringValue(value.PrivateIP)
	model.PublicIP = types.StringValue(value.PublicIP)
	model.MAC = types.StringValue(value.MAC)
	model.Status = types.StringValue(value.Status)
	model.LastError = types.StringValue(value.LastError)
	model.Tags = common.TagsToTerraform(ctx, value.Tags, diags)
}
