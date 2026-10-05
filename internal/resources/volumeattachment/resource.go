// Package volumeattachment implements the asc_volume_attachment resource.
package volumeattachment

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	AttachVolume(context.Context, string, string) (client.Volume, error)
	GetVolumeAttachment(context.Context, string, string) (client.Volume, error)
	DetachVolume(context.Context, string, string) error
}

type Resource struct {
	client resourceClient
}

// Model identifies an attachment by both endpoint names and records its volume state.
type Model struct {
	VolumeName types.String `tfsdk:"volume_name"`
	VMName     types.String `tfsdk:"vm_name"`
	Status     types.String `tfsdk:"status"`
	LastError  types.String `tfsdk:"last_error"`
}

func NewResource() resource.Resource { return &Resource{} }

func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_attachment"
}

func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Attaches an ASC block volume to a virtual machine.",
		Attributes: map[string]schema.Attribute{
			"volume_name": schema.StringAttribute{
				Description:   "Name of the volume to attach.",
				Required:      true,
				PlanModifiers: replaceString,
			},
			"vm_name": schema.StringAttribute{
				Description: "Name of the virtual machine to attach the volume to. Moving a volume " +
					"to another machine detaches it and attaches it again, so this forces replacement.",
				Required:      true,
				PlanModifiers: replaceString,
			},
			"status":     schema.StringAttribute{Description: "Status the volume reports while attached.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the volume, empty when there is none.", Computed: true},
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

	attached, err := r.client.AttachVolume(ctx, plan.VolumeName.ValueString(), plan.VMName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error attaching volume", err.Error())
		return
	}
	setFromVolume(&plan, attached)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read removes the attachment from state if either endpoint is gone or the volume is
// attached elsewhere.
func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetVolumeAttachment(ctx, state.VolumeName.ValueString(), state.VMName.ValueString())
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume attachment", err.Error())
		return
	}
	setFromVolume(&state, current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is a no-op: both names force replacement, so an attachment is only
// ever created or destroyed.
func (r *Resource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DetachVolume(ctx, state.VolumeName.ValueString(), state.VMName.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error detaching volume", err.Error())
	}
}

// ImportState takes volume-name:vm-name, because neither name identifies an
// attachment on its own.
func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	separator := strings.Index(req.ID, ":")
	if separator <= 0 || separator == len(req.ID)-1 {
		resp.Diagnostics.AddError("Invalid volume attachment import identifier", "Use volume-name:vm-name.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("volume_name"), req.ID[:separator])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vm_name"), req.ID[separator+1:])...)
}

// setFromVolume preserves both configured names; attachedToVM may contain a UUID rather
// than the VM name.
func setFromVolume(model *Model, value client.Volume) {
	model.Status = types.StringValue(value.Status)
	model.LastError = types.StringValue(value.LastError)
}
