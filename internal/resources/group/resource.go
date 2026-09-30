// Package group implements the asc_group resource.
package group

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	CreateGroup(context.Context, string) (client.Group, error)
	GetGroup(context.Context, string) (client.Group, error)
	DeleteGroup(context.Context, string) error
}

// Resource manages one UAM tenant group.
type Resource struct {
	client resourceClient
}

// Model accepts only a group name; UAM has no group update route.
type Model struct {
	Name        types.String `tfsdk:"name"`
	ID          types.String `tfsdk:"id"`
	URN         types.String `tfsdk:"urn"`
	Description types.String `tfsdk:"description"`
	Members     types.List   `tfsdk:"members"`
}

// MemberTypes is the shape of one group member, shared with the data source.
var MemberTypes = map[string]attr.Type{
	"name": types.StringType, "enabled": types.BoolType, "type": types.StringType,
}

// NewResource creates the asc_group resource.
func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a group in the configured tenant.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "Name of the group. Changing the name replaces the group.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(3)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"id":  schema.StringAttribute{Description: "Identifier of the group.", Computed: true},
			"urn": schema.StringAttribute{Description: "Resource identifier of the group.", Computed: true},
			"description": schema.StringAttribute{
				Description: "Description of the group. Read-only.",
				Computed:    true,
			},
			"members": schema.ListNestedAttribute{
				Description: "Users and API clients that belong to the group, sorted by name. " +
					"Membership cannot be changed through this provider.",
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":    schema.StringAttribute{Description: "Username or API client name of the member.", Computed: true},
					"enabled": schema.BoolAttribute{Description: "Whether the member is enabled.", Computed: true},
					"type":    schema.StringAttribute{Description: "Whether the member is a user or an API client.", Computed: true},
				}},
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
	created, err := r.client.CreateGroup(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating group", err.Error())
		return
	}
	SetFromGroup(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetGroup(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading group", err.Error())
		return
	}
	SetFromGroup(ctx, &state, current, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable: name changes require replacement and other fields are computed.
func (r *Resource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGroup(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error deleting group", err.Error())
	}
}

// ImportState recovers a group from its name, which is how every UAM route
// addresses it.
func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// SetFromGroup writes an authoritative read into the model. It is exported so
// the data source shares one mapping with the resource.
func SetFromGroup(ctx context.Context, model *Model, group client.Group, diagnostics *diag.Diagnostics) {
	model.Name = types.StringValue(group.Name)
	model.ID = types.StringValue(group.ID)
	model.URN = types.StringValue(group.URN)
	model.Description = types.StringValue(group.Description)
	model.Members = MembersToList(ctx, group.Members, diagnostics)
}

// MembersToList converts the read model's members into the list both the
// resource and the data source expose.
func MembersToList(ctx context.Context, members []client.GroupMember, diagnostics *diag.Diagnostics) types.List {
	values := make([]attr.Value, 0, len(members))
	for _, member := range members {
		value, diags := types.ObjectValue(MemberTypes, map[string]attr.Value{
			"name":    types.StringValue(member.Name),
			"enabled": types.BoolValue(member.Enabled),
			"type":    types.StringValue(member.Type),
		})
		diagnostics.Append(diags...)
		values = append(values, value)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: MemberTypes}, values)
	diagnostics.Append(diags...)
	return list
}
