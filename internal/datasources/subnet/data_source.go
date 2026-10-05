// Package subnet implements the asc_subnet data source.
package subnet

import (
	"context"
	"errors"
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

// GetSubnet searches the VPC collection because there is no single-subnet GET route.
type dataSourceClient interface {
	GetSubnet(context.Context, string, string) (client.Subnet, error)
}

// DataSource reads a single subnet of a VPC from infra-gateway by name.
type DataSource struct {
	client dataSourceClient
}

// Model mirrors the asc_subnet resource attributes.
type Model struct {
	ID        types.String `tfsdk:"id"`
	URN       types.String `tfsdk:"urn"`
	Name      types.String `tfsdk:"name"`
	VPCName   types.String `tfsdk:"vpc_name"`
	VPCID     types.String `tfsdk:"vpc_id"`
	CIDR      types.String `tfsdk:"cidr"`
	Type      types.String `tfsdk:"type"`
	Status    types.String `tfsdk:"status"`
	LastError types.String `tfsdk:"last_error"`
	Tags      types.Map    `tfsdk:"tags"`
}

// NewDataSource creates the asc_subnet data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads a subnet belonging to an ASC VPC.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Description: "Identifier of the subnet.", Computed: true},
			"urn":        schema.StringAttribute{Description: "URN of the subnet.", Computed: true},
			"name":       schema.StringAttribute{Description: "Name of the subnet to read.", Required: true},
			"vpc_name":   schema.StringAttribute{Description: "Name of the VPC that owns the subnet.", Required: true},
			"vpc_id":     schema.StringAttribute{Description: "Identifier of the owning VPC.", Computed: true},
			"cidr":       schema.StringAttribute{Description: "CIDR block of the subnet.", Computed: true},
			"type":       schema.StringAttribute{Description: "Subnet type, public or private.", Computed: true},
			"status":     schema.StringAttribute{Description: "Current lifecycle status.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the subnet, empty when healthy.", Computed: true},
			"tags":       schema.MapAttribute{Description: "Tags applied to the subnet.", Computed: true, ElementType: types.StringType},
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

	vpcName, name := config.VPCName.ValueString(), config.Name.ValueString()
	current, err := d.client.GetSubnet(ctx, vpcName, name)
	// A missing subnet or parent VPC is an error for a configured data source.
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("Subnet not found", fmt.Sprintf("No subnet named %q exists in VPC %q.", name, vpcName))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read subnet", fmt.Sprintf("Reading subnet %q of VPC %q failed: %s", name, vpcName, err))
		return
	}

	state := Model{
		ID:        types.StringValue(current.ID),
		URN:       types.StringValue(current.URN),
		Name:      types.StringValue(current.Name),
		VPCName:   config.VPCName,
		VPCID:     types.StringValue(current.VPCID),
		CIDR:      types.StringValue(current.CIDR),
		Type:      types.StringValue(current.Type),
		Status:    types.StringValue(current.Status),
		LastError: types.StringValue(current.LastError),
		Tags:      common.TagsToTerraform(ctx, current.Tags, &resp.Diagnostics),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
