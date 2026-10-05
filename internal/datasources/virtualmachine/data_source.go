// Package virtualmachine implements the asc_virtual_machine data source.
package virtualmachine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/common"
)

var (
	_ datasource.DataSource              = &DataSource{}
	_ datasource.DataSourceWithConfigure = &DataSource{}
)

type dataSourceClient interface {
	GetVM(context.Context, string) (client.VM, error)
}

// DataSource reads a single virtual machine from infra-gateway by name.
type DataSource struct {
	client dataSourceClient
}

// Model exposes readable VM fields. Image and SKU ID are available through os and sku;
// customization is write-only.
type Model struct {
	URN             types.String `tfsdk:"urn"`
	Name            types.String `tfsdk:"name"`
	SKU             types.Object `tfsdk:"sku"`
	VPCID           types.String `tfsdk:"vpc_id"`
	Status          types.String `tfsdk:"status"`
	LastError       types.String `tfsdk:"last_error"`
	Tags            types.Map    `tfsdk:"tags"`
	AttachedVolumes types.List   `tfsdk:"attached_volumes"`
	OS              types.Object `tfsdk:"os"`
	EnableLogging   types.Bool   `tfsdk:"enable_logging"`
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

// NewDataSource creates the asc_virtual_machine data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
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
		Description: "Reads an ASC virtual machine.",
		Attributes: map[string]schema.Attribute{
			"urn":        schema.StringAttribute{Description: "URN of the virtual machine.", Computed: true},
			"name":       schema.StringAttribute{Description: "Name of the virtual machine to read.", Required: true},
			"sku":        schema.SingleNestedAttribute{Description: "SKU the virtual machine runs on.", Computed: true, Attributes: computedSKU},
			"vpc_id":     schema.StringAttribute{Description: "Identifier of the VPC the virtual machine belongs to.", Computed: true},
			"status":     schema.StringAttribute{Description: "Current lifecycle status.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the virtual machine, empty when healthy.", Computed: true},
			"tags":       schema.MapAttribute{Description: "Tags applied to the virtual machine.", Computed: true, ElementType: types.StringType},
			"attached_volumes": schema.ListAttribute{
				Description: "Names of the attached volumes, sorted.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"os":             schema.SingleNestedAttribute{Description: "Operating system image the virtual machine boots.", Computed: true, Attributes: computedOS},
			"enable_logging": schema.BoolAttribute{Description: "Whether platform logging is enabled for the virtual machine.", Computed: true},
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
	var config Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := config.Name.ValueString()
	current, err := d.client.GetVM(ctx, name)
	// Data sources require the named object to exist; a 404 is a configuration error.
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Virtual machine not found", fmt.Sprintf("No virtual machine named %q exists in this tenant.", name))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read virtual machine", fmt.Sprintf("Reading virtual machine %q failed: %s", name, err))
		return
	}

	// Sort a copy so map iteration order cannot change Terraform state.
	volumes := slices.Clone(current.AttachedVolumes)
	slices.Sort(volumes)

	attachedVolumes, volumeDiags := types.ListValueFrom(ctx, types.StringType, volumes)
	resp.Diagnostics.Append(volumeDiags...)

	sku, skuDiags := types.ObjectValueFrom(ctx, skuTypes, skuModel{
		ID: types.StringValue(current.SKU.ID), Name: types.StringValue(current.SKU.Name), Family: types.StringValue(current.SKU.Family),
		Threads: types.Int64Value(int64(current.SKU.Threads)), Cores: types.Int64Value(int64(current.SKU.Cores)),
		MemoryInMB: types.Int64Value(int64(current.SKU.MemoryInMB)), StorageInGB: types.Int64Value(int64(current.SKU.StorageInGB)),
		StorageType: types.StringValue(current.SKU.StorageType), GPUCount: types.Int64Value(int64(current.SKU.GPUCount)),
		GPUType: types.StringValue(current.SKU.GPUType),
	})
	resp.Diagnostics.Append(skuDiags...)

	osValue, osDiags := types.ObjectValueFrom(ctx, osTypes, osModel{
		ID: types.StringValue(current.OS.ID), Family: types.StringValue(current.OS.Family),
		Distribution: types.StringValue(current.OS.Distribution), Release: types.StringValue(current.OS.Release),
		DisplayName: types.StringValue(current.OS.DisplayName),
	})
	resp.Diagnostics.Append(osDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := Model{
		URN:             types.StringValue(current.URN),
		Name:            types.StringValue(current.Name),
		SKU:             sku,
		VPCID:           types.StringValue(current.VPCID),
		Status:          types.StringValue(current.Status),
		LastError:       types.StringValue(current.LastError),
		Tags:            common.TagsToTerraform(ctx, current.Tags, &resp.Diagnostics),
		AttachedVolumes: attachedVolumes,
		OS:              osValue,
		EnableLogging:   types.BoolValue(current.EnableLogging),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
