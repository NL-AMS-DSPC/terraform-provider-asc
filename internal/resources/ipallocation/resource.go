// Package ipallocation implements the asc_ip_allocation resource
package ipallocation

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

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
	AllocateIP(context.Context, string, string) (client.VM, error)
	GetIPAllocation(context.Context, string, string) (client.VM, error)
	DeallocateIP(context.Context, string, string) error
}

// Resource allocates a reserved IP address to a virtual machine as a public IP
type Resource struct {
	client resourceClient
}

// Model identifies an allocation by the machine name and the address
type Model struct {
	VMName types.String `tfsdk:"vm_name"`
	IP     types.String `tfsdk:"ip"`
}

// NewResource creates the asc_ip_allocation resource.
func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip_allocation"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Allocates a reserved IP address to a virtual machine as a public IP.",
		Attributes: map[string]schema.Attribute{
			"vm_name": schema.StringAttribute{
				Description: "Name of the virtual machine to allocate the address to. Moving an " +
					"address to another machine deallocates it and allocates it again, so this forces replacement.",
				Required:      true,
				PlanModifiers: replaceString,
			},
			"ip": schema.StringAttribute{
				Description:   "IPv4 address to allocate. It must already be reserved, typically by asc_ip.",
				Required:      true,
				PlanModifiers: replaceString,
				Validators:    []validator.String{ipv4Validator{}},
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

	if _, err := r.client.AllocateIP(ctx, plan.VMName.ValueString(), plan.IP.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error allocating IP address", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read removes the allocation from state if the machine is gone or no longer lists the address
func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.GetIPAllocation(ctx, state.VMName.ValueString(), state.IP.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading IP allocation", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is a no-op: both attributes force replacement, so an allocation is only ever created or destroyed
func (r *Resource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeallocateIP(ctx, state.VMName.ValueString(), state.IP.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error deallocating IP address", err.Error())
	}
}

// ImportState takes vm-name:ip. Only IPv4 is supported and VM names cannot contain a colon, so the first one separates the two
func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	separator := strings.Index(req.ID, ":")
	if separator <= 0 || separator == len(req.ID)-1 {
		resp.Diagnostics.AddError("Invalid IP allocation import identifier", "Use vm-name:ip.")
		return
	}
	ip := req.ID[separator+1:]
	if !isValidIP(ip) {
		resp.Diagnostics.AddError("Invalid IP address", fmt.Sprintf("%q, must be an IPv4 address.", ip))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vm_name"), req.ID[:separator])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ip"), ip)...)
}

// ipv4Validator implements the schema String interface and enforces IPV4 addresses that are equal to the configured address
type ipv4Validator struct{}

func (ipv4Validator) Description(context.Context) string { return "must be an IPv4 address" }

func (v ipv4Validator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v ipv4Validator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	if !isValidIP(value) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP address", fmt.Sprintf("%q %s.", value, v.Description(ctx)))
	}
}

func isValidIP(ip string) bool {
	address, err := netip.ParseAddr(ip)
	return err == nil && address.Is4() && address.String() == ip
}
