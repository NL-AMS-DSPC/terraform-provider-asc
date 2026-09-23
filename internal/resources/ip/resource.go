// Package ip implements the asc_ip resource.
package ip

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

var (
	_ resource.Resource                = &Resource{}
	_ resource.ResourceWithConfigure   = &Resource{}
	_ resource.ResourceWithImportState = &Resource{}
)

type resourceClient interface {
	ReserveIP(context.Context) (client.Reservation, error)
	GetReservation(context.Context, string) (client.Reservation, error)
	ReleaseIP(context.Context, string) error
}

// Resource reserves a single, randomly assigned IP address through IPAM.
type Resource struct {
	client resourceClient
}

// Model has no configurable attributes: IPAM picks the address, and a new
// reservation is the only way to get a different one.
type Model struct {
	IP         types.String `tfsdk:"ip"`
	ReservedAt types.String `tfsdk:"reserved_at"`
}

// NewResource creates the asc_ip resource.
func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Reserves a random IP address.",
		Attributes: map[string]schema.Attribute{
			"ip": schema.StringAttribute{
				Description:   "IP address reserved.",
				Computed:      true,
				PlanModifiers: keep,
			},
			"reserved_at": schema.StringAttribute{
				Description:   "Time at which the address was reserved.",
				Computed:      true,
				PlanModifiers: keep,
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

	created, err := r.client.ReserveIP(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reserving IP address", err.Error())
		return
	}
	setFromReservation(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetReservation(ctx, state.IP.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading IP reservation", err.Error())
		return
	}
	setFromReservation(&state, current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update does nothing since every attribute is computed
func (r *Resource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ReleaseIP(ctx, state.IP.ValueString()); err != nil && !errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Error releasing IP address", err.Error())
	}
}

// ImportState recovers a reservation from its IP address
func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

func setFromReservation(model *Model, reservation client.Reservation) {
	model.IP = types.StringValue(reservation.IP)
	model.ReservedAt = types.StringValue(reservation.ReservedAt)
}
