// Package vmgroup implements the asc_vm_group data source.
package vmgroup

import (
	"context"
	"fmt"

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

// DataSource reads one virtual machine group from infra-gateway.
type DataSource struct {
	client *client.Runtime
}

// Model omits VPC ID, image and customization because the gateway never returns them.
type Model struct {
	Name            types.String      `tfsdk:"name"`
	URN             types.String      `tfsdk:"urn"`
	SKU             *skuModel         `tfsdk:"sku"`
	Status          types.String      `tfsdk:"status"`
	LastError       types.String      `tfsdk:"last_error"`
	Tags            types.Map         `tfsdk:"tags"`
	AttachedVolumes types.List        `tfsdk:"attached_volumes"`
	Autoscaling     *autoscalingModel `tfsdk:"autoscaling"`
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

type autoscalingModel struct {
	MinReplicas types.Int64 `tfsdk:"min_replicas"`
	MaxReplicas types.Int64 `tfsdk:"max_replicas"`
	Cron        *cronModel  `tfsdk:"cron"`
}

type cronModel struct {
	Timezone        types.String `tfsdk:"timezone"`
	Start           types.String `tfsdk:"start"`
	End             types.String `tfsdk:"end"`
	DesiredReplicas types.Int64  `tfsdk:"desired_replicas"`
}

// NewDataSource creates the asc_vm_group data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_group"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads a virtual machine group by name.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Description: "Name of the virtual machine group.", Required: true},
			"urn":  schema.StringAttribute{Description: "Resource identifier of the group.", Computed: true},
			"sku": schema.SingleNestedAttribute{
				Description: "SKU backing every member of the group.",
				Computed:    true,
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
			"status":     schema.StringAttribute{Description: "Lifecycle status reported by ASC.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the group, empty when there is none.", Computed: true},
			"tags":       schema.MapAttribute{Description: "Tags on the group.", Computed: true, ElementType: types.StringType},
			"attached_volumes": schema.ListAttribute{
				Description: "Names of the volumes attached to the group, sorted.",
				Computed:    true, ElementType: types.StringType,
			},
			"autoscaling": schema.SingleNestedAttribute{
				Description: "Scaling bounds of the group.",
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"min_replicas": schema.Int64Attribute{Description: "Lower replica bound.", Computed: true},
					"max_replicas": schema.Int64Attribute{Description: "Upper replica bound.", Computed: true},
					"cron": schema.SingleNestedAttribute{
						Description: "Cron window overriding the replica count, null when the group has none.",
						Computed:    true,
						Attributes: map[string]schema.Attribute{
							"timezone":         schema.StringAttribute{Description: "IANA timezone the window is expressed in.", Computed: true},
							"start":            schema.StringAttribute{Description: "Cron expression opening the window.", Computed: true},
							"end":              schema.StringAttribute{Description: "Cron expression closing the window.", Computed: true},
							"desired_replicas": schema.Int64Attribute{Description: "Replica count held while the window is open.", Computed: true},
						},
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

func (d *DataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := d.client.GetVMGroup(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read virtual machine group", err.Error())
		return
	}

	state.URN = types.StringValue(group.URN)
	state.Name = types.StringValue(group.Name)
	state.Status = types.StringValue(group.Status)
	state.LastError = types.StringValue(group.LastError)
	state.Tags = common.TagsToTerraform(ctx, group.Tags, &resp.Diagnostics)
	state.SKU = &skuModel{
		ID: types.StringValue(group.SKU.ID), Name: types.StringValue(group.SKU.Name), Family: types.StringValue(group.SKU.Family),
		Threads: types.Int64Value(int64(group.SKU.Threads)), Cores: types.Int64Value(int64(group.SKU.Cores)),
		MemoryInMB: types.Int64Value(int64(group.SKU.MemoryInMB)), StorageInGB: types.Int64Value(int64(group.SKU.StorageInGB)),
		StorageType: types.StringValue(group.SKU.StorageType), GPUCount: types.Int64Value(int64(group.SKU.GPUCount)),
		GPUType: types.StringValue(group.SKU.GPUType),
	}
	state.Autoscaling = &autoscalingModel{
		MinReplicas: types.Int64Value(group.AutoscalingPolicy.MinReplicas),
		MaxReplicas: types.Int64Value(group.AutoscalingPolicy.MaxReplicas),
	}
	if group.AutoscalingPolicy.Cron != nil {
		state.Autoscaling.Cron = &cronModel{
			Timezone: types.StringValue(group.AutoscalingPolicy.Cron.Timezone), Start: types.StringValue(group.AutoscalingPolicy.Cron.Start),
			End: types.StringValue(group.AutoscalingPolicy.Cron.End), DesiredReplicas: types.Int64Value(group.AutoscalingPolicy.Cron.DesiredReplicas),
		}
	}

	volumes, volumeDiags := types.ListValueFrom(ctx, types.StringType, group.AttachedVolumes)
	resp.Diagnostics.Append(volumeDiags...)
	state.AttachedVolumes = volumes

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
