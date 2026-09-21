// Package sku implements the asc_skus data source.
package sku

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

var (
	_ datasource.DataSource              = &DataSource{}
	_ datasource.DataSourceWithConfigure = &DataSource{}
)

// DataSource reads the authoritative SKU catalogue from infra-gateway.
type DataSource struct {
	client *client.Runtime
}

// Model uses the same attribute names as the VM resource's nested SKU.
type Model struct {
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

// DataSourceModel is the top level state of the data source.
type DataSourceModel struct {
	SKUs []Model `tfsdk:"skus"`
}

// NewDataSource creates the asc_skus data source.
func NewDataSource() datasource.DataSource {
	return &DataSource{}
}

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skus"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the SKUs available to the tenant.",
		Attributes: map[string]schema.Attribute{
			"skus": schema.ListNestedAttribute{
				Description: "Available SKUs, ordered by id. infra-gateway does not " +
					"guarantee a stable order, so the list is sorted here to keep " +
					"state and plans stable across reads.",
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
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
					},
				},
			},
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

func (d *DataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	skus, err := d.client.ListSKUs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list SKUs", err.Error())
		return
	}

	// Sort by ID to prevent catalogue ordering from producing state diffs.
	slices.SortFunc(skus, func(a, b client.SKU) int { return cmp.Compare(a.ID, b.ID) })

	state := DataSourceModel{SKUs: make([]Model, len(skus))}
	for i, sku := range skus {
		state.SKUs[i] = Model{
			ID:          types.StringValue(sku.ID),
			Name:        types.StringValue(sku.Name),
			Family:      types.StringValue(sku.Family),
			Threads:     types.Int64Value(int64(sku.Threads)),
			Cores:       types.Int64Value(int64(sku.Cores)),
			MemoryInMB:  types.Int64Value(int64(sku.MemoryInMB)),
			StorageInGB: types.Int64Value(int64(sku.StorageInGB)),
			StorageType: types.StringValue(sku.StorageType),
			GPUCount:    types.Int64Value(int64(sku.GPUCount)),
			GPUType:     types.StringValue(sku.GPUType),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
