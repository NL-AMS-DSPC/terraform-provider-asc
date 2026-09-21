// Package role implements the asc_role data source.
package role

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

var (
	_ datasource.DataSource              = &DataSource{}
	_ datasource.DataSourceWithConfigure = &DataSource{}
)

// DataSource reads one role from the configured UAM tenant.
type DataSource struct {
	client *client.Runtime
}

// Model keys roles by name because UAM list and item routes disagree on ID.
type Model struct {
	Name    types.String `tfsdk:"name"`
	URN     types.String `tfsdk:"urn"`
	Actions types.Set    `tfsdk:"actions"`
}

// NewDataSource creates the asc_role data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads a role by name from the configured UAM tenant.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Description: "Name of the role.", Required: true},
			"urn":  schema.StringAttribute{Description: "Resource identifier of the role.", Computed: true},
			"actions": schema.SetAttribute{
				Description: "Actions the role grants, which UAM reports as its composites.",
				Computed:    true, ElementType: types.StringType,
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
	var config Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := d.client.GetRole(ctx, config.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading role", err.Error())
		return
	}
	actions, diags := types.SetValueFrom(ctx, types.StringType, role.Actions)
	resp.Diagnostics.Append(diags...)
	config.URN = types.StringValue(role.URN)
	config.Actions = actions
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
