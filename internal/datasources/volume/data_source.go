// Package volume implements the asc_volume data source.
package volume

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/common"
)

var (
	_ datasource.DataSource              = &DataSource{}
	_ datasource.DataSourceWithConfigure = &DataSource{}
)

// DataSource reads one block volume from infra-gateway.
type DataSource struct {
	client *client.Runtime
}

// Model mirrors the asc_volume resource attributes.
type Model struct {
	Name         types.String `tfsdk:"name"`
	Size         types.String `tfsdk:"size"`
	SizeBytes    types.Int64  `tfsdk:"size_bytes"`
	VolumeMode   types.String `tfsdk:"volume_mode"`
	URN          types.String `tfsdk:"urn"`
	Status       types.String `tfsdk:"status"`
	LastError    types.String `tfsdk:"last_error"`
	AttachedToVM types.String `tfsdk:"attached_to_vm"`
	Tags         types.Map    `tfsdk:"tags"`
	Labels       types.Map    `tfsdk:"labels"`
	Annotations  types.Map    `tfsdk:"annotations"`
}

// NewDataSource creates the asc_volume data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads an ASC block volume.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Description: "Name of the volume.", Required: true},
			"size": schema.StringAttribute{Description: "Reported capacity, for example \"10Gi\".", Computed: true},
			"size_bytes": schema.Int64Attribute{
				Description: "Capacity in bytes. The reported size may use equivalent spellings " +
					"between reads, so this is the value to compare against.",
				Computed: true,
			},
			"volume_mode":    schema.StringAttribute{Description: "Volume mode.", Computed: true},
			"urn":            schema.StringAttribute{Description: "URN of the volume, which carries its uuid.", Computed: true},
			"status":         schema.StringAttribute{Description: "Current lifecycle status.", Computed: true},
			"last_error":     schema.StringAttribute{Description: "Last error reported for the volume, empty when there is none.", Computed: true},
			"attached_to_vm": schema.StringAttribute{Description: "UUID of the virtual machine the volume is attached to, empty when detached.", Computed: true},
			"tags":           schema.MapAttribute{Description: "Tags applied to the volume.", Computed: true, ElementType: types.StringType},
			"labels":         schema.MapAttribute{Description: "Labels of the volume.", Computed: true, ElementType: types.StringType},
			"annotations":    schema.MapAttribute{Description: "Annotations of the volume.", Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *DataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	runtime, ok := req.ProviderData.(*client.Runtime)
	if !ok {
		resp.Diagnostics.AddError("Unexpected data source configuration", fmt.Sprintf("Expected *client.Runtime, got %T.", req.ProviderData))
		return
	}
	d.client = runtime
}

func (d *DataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	current, err := d.client.GetVolume(ctx, name)
	if errors.Is(err, client.ErrResourceNotFound) {
		// A data source names a volume the configuration depends on, so an
		// absent one is a configuration error rather than drift to reconcile.
		resp.Diagnostics.AddError("Volume not found", fmt.Sprintf("No volume found named %q.", name))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read volume", err.Error())
		return
	}

	size, err := client.ParseQuantity(current.Size)
	if err != nil {
		resp.Diagnostics.AddError("Unsupported volume size", fmt.Sprintf("ASC reported size %q for volume %q: %s", current.Size, name, err))
		return
	}

	state = Model{
		Name: types.StringValue(current.Name), Size: types.StringValue(current.Size), SizeBytes: types.Int64Value(size),
		VolumeMode: types.StringValue(current.VolumeMode), URN: types.StringValue(current.URN),
		Status: types.StringValue(current.Status), LastError: types.StringValue(current.LastError),
		AttachedToVM: types.StringValue(current.AttachedToVM),
		Tags:         common.TagsToTerraform(ctx, current.Tags, &resp.Diagnostics),
		Labels:       stringMapToTerraform(ctx, current.Labels, &resp.Diagnostics),
		Annotations:  stringMapToTerraform(ctx, current.Annotations, &resp.Diagnostics),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func stringMapToTerraform(ctx context.Context, values map[string]string, diags *diag.Diagnostics) types.Map {
	if len(values) == 0 {
		return types.MapNull(types.StringType)
	}
	result, conversionDiags := types.MapValueFrom(ctx, types.StringType, values)
	diags.Append(conversionDiags...)
	return result
}
