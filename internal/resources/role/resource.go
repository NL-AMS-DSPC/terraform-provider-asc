// Package role implements the asc_role resource.
package role

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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
)

var (
	_ resource.Resource                = &Resource{}
	_ resource.ResourceWithConfigure   = &Resource{}
	_ resource.ResourceWithImportState = &Resource{}
)

type resourceClient interface {
	CreateRole(context.Context, string, []string) (client.Role, error)
	GetRole(context.Context, string) (client.Role, error)
	UpdateRole(context.Context, string, []string) (client.Role, error)
	DeleteRole(context.Context, string) error
}

// Resource manages one UAM tenant role.
type Resource struct {
	client resourceClient
}

// Model keys a role by name; UAM list and item routes disagree on ID.
type Model struct {
	Name    types.String `tfsdk:"name"`
	URN     types.String `tfsdk:"urn"`
	Actions types.Set    `tfsdk:"actions"`
}

// NewResource creates the asc_role resource.
func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a role in the configured UAM tenant.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description: "Name of the role, which is also its identity. UAM's update addresses a " +
					"role by the name in the request body, so a role cannot be renamed and a new name " +
					"replaces it.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 50)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"urn": schema.StringAttribute{Description: "Resource identifier of the role.", Computed: true},
			"actions": schema.SetAttribute{
				Description: "Actions the role grants. UAM accepts them as \"actions\" and reports them " +
					"back as \"composites\"; the order is not preserved, so this is a set.",
				Required:    true,
				ElementType: types.StringType,
				Validators:  []validator.Set{setvalidator.SizeBetween(1, 50)},
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
	created, err := r.client.CreateRole(ctx, plan.Name.ValueString(), actionsToClient(ctx, plan.Actions, &resp.Diagnostics))
	if resp.Diagnostics.HasError() {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating role", err.Error())
		return
	}
	setFromRole(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetRole(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading role", err.Error())
		return
	}
	setFromRole(ctx, &state, current, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes actions; renaming requires replacement.
func (r *Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.client.UpdateRole(ctx, plan.Name.ValueString(), actionsToClient(ctx, plan.Actions, &resp.Diagnostics))
	if resp.Diagnostics.HasError() {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating role", err.Error())
		return
	}
	setFromRole(ctx, &plan, updated, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRole(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting role", err.Error())
	}
}

// ImportState identifies the role by name.
func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func setFromRole(ctx context.Context, model *Model, role client.Role, diagnostics *diag.Diagnostics) {
	model.Name = types.StringValue(role.Name)
	model.URN = types.StringValue(role.URN)
	actions, diags := types.SetValueFrom(ctx, types.StringType, role.Actions)
	diagnostics.Append(diags...)
	model.Actions = actions
}

func actionsToClient(ctx context.Context, value types.Set, diagnostics *diag.Diagnostics) []string {
	var actions []string
	diagnostics.Append(value.ElementsAs(ctx, &actions, false)...)
	return actions
}
