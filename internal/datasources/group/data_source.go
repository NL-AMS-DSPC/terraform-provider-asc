// Package group implements the asc_group data source.
package group

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
	groupresource "github.com/nl-ams-dspc/terraform-provider-asc/internal/resources/group"
)

var (
	_ datasource.DataSource              = &DataSource{}
	_ datasource.DataSourceWithConfigure = &DataSource{}
)

// DataSource reads one group from the configured UAM tenant.
type DataSource struct {
	client *client.Runtime
}

// Model exposes what UAM serves for a group. The members come from the
// single-item read, which is the only route that carries them.
type Model struct {
	Name        types.String `tfsdk:"name"`
	ID          types.String `tfsdk:"id"`
	URN         types.String `tfsdk:"urn"`
	Description types.String `tfsdk:"description"`
	Members     types.List   `tfsdk:"members"`
}

// NewDataSource creates the asc_group data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads a group by name from the configured UAM tenant.",
		Attributes: map[string]schema.Attribute{
			"name":        schema.StringAttribute{Description: "Name of the group.", Required: true},
			"id":          schema.StringAttribute{Description: "Identifier UAM records for the group.", Computed: true},
			"urn":         schema.StringAttribute{Description: "Resource identifier of the group.", Computed: true},
			"description": schema.StringAttribute{Description: "Description UAM records for the group.", Computed: true},
			"members": schema.ListNestedAttribute{
				Description: "Users and API clients that belong to the group, sorted by name.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":    schema.StringAttribute{Description: "Username or API client name of the member.", Computed: true},
					"enabled": schema.BoolAttribute{Description: "Whether the member is enabled.", Computed: true},
					"type":    schema.StringAttribute{Description: "Whether the member is a user or an API client.", Computed: true},
				}},
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
	group, err := d.client.GetGroup(ctx, config.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading group", err.Error())
		return
	}
	config.ID = types.StringValue(group.ID)
	config.URN = types.StringValue(group.URN)
	config.Description = types.StringValue(group.Description)
	config.Members = groupresource.MembersToList(ctx, group.Members, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
