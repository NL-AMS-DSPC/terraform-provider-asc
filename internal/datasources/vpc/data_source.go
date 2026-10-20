// Package vpc implements the asc_vpc data source.
package vpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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

type dataSourceClient interface {
	GetVPC(context.Context, string) (client.VPC, error)
}

// DataSource reads a single VPC from infra-gateway by name.
type DataSource struct {
	client dataSourceClient
}

// Model mirrors the asc_vpc resource attributes.
type Model struct {
	ID        types.String `tfsdk:"id"`
	URN       types.String `tfsdk:"urn"`
	Name      types.String `tfsdk:"name"`
	CIDR      types.String `tfsdk:"cidr"`
	Status    types.String `tfsdk:"status"`
	LastError types.String `tfsdk:"last_error"`
	Subnets   types.List   `tfsdk:"subnets"`
	Tags      types.Map    `tfsdk:"tags"`
}

type inlineSubnetModel struct {
	ID        types.String `tfsdk:"id"`
	URN       types.String `tfsdk:"urn"`
	Name      types.String `tfsdk:"name"`
	VPCID     types.String `tfsdk:"vpc_id"`
	CIDR      types.String `tfsdk:"cidr"`
	Status    types.String `tfsdk:"status"`
	LastError types.String `tfsdk:"last_error"`
	Tags      types.Map    `tfsdk:"tags"`
}

var inlineSubnetTypes = map[string]attr.Type{
	"id":         types.StringType,
	"urn":        types.StringType,
	"name":       types.StringType,
	"vpc_id":     types.StringType,
	"cidr":       types.StringType,
	"status":     types.StringType,
	"last_error": types.StringType,
	"tags":       types.MapType{ElemType: types.StringType},
}

// NewDataSource creates the asc_vpc data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads an ASC VPC.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Description: "Identifier of the VPC.", Computed: true},
			"urn":        schema.StringAttribute{Description: "URN of the VPC.", Computed: true},
			"name":       schema.StringAttribute{Description: "Name of the VPC to read.", Required: true},
			"cidr":       schema.StringAttribute{Description: "CIDR block of the VPC.", Computed: true},
			"status":     schema.StringAttribute{Description: "Current lifecycle status.", Computed: true},
			"last_error": schema.StringAttribute{Description: "Last error reported for the VPC, empty when healthy.", Computed: true},
			"subnets": schema.ListNestedAttribute{
				// The event stream preserves subnet creation order.
				Description: "Subnets belonging to the VPC. Order is not guaranteed.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":         schema.StringAttribute{Description: "Identifier of the subnet.", Computed: true},
					"urn":        schema.StringAttribute{Description: "URN of the subnet.", Computed: true},
					"name":       schema.StringAttribute{Description: "Name of the subnet.", Computed: true},
					"vpc_id":     schema.StringAttribute{Description: "Identifier of the owning VPC.", Computed: true},
					"cidr":       schema.StringAttribute{Description: "CIDR block of the subnet.", Computed: true},
					"status":     schema.StringAttribute{Description: "Lifecycle status of the subnet.", Computed: true},
					"last_error": schema.StringAttribute{Description: "Last error reported for the subnet, empty when healthy.", Computed: true},
					"tags":       schema.MapAttribute{Description: "Tags applied to the subnet.", Computed: true, ElementType: types.StringType},
				}},
			},
			"tags": schema.MapAttribute{Description: "Tags applied to the VPC.", Computed: true, ElementType: types.StringType},
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
	current, err := d.client.GetVPC(ctx, name)
	// Data sources require the named object to exist; a 404 is a configuration error.
	if errors.Is(err, client.ErrResourceNotFound) {
		resp.Diagnostics.AddError("VPC not found", fmt.Sprintf("No VPC named %q exists in this tenant.", name))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VPC", fmt.Sprintf("Reading VPC %q failed: %s", name, err))
		return
	}

	state := Model{
		ID:        types.StringValue(current.ID),
		URN:       types.StringValue(current.URN),
		Name:      types.StringValue(current.Name),
		CIDR:      types.StringValue(current.CIDR),
		Status:    types.StringValue(current.Status),
		LastError: types.StringValue(current.LastError),
		Subnets:   inlineSubnetsToTerraform(ctx, current.Subnets, &resp.Diagnostics),
		Tags:      common.TagsToTerraform(ctx, current.Tags, &resp.Diagnostics),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func inlineSubnetsToTerraform(ctx context.Context, values []client.Subnet, diags *diag.Diagnostics) types.List {
	models := make([]inlineSubnetModel, 0, len(values))
	for _, subnet := range values {
		models = append(models, inlineSubnetModel{
			ID: types.StringValue(subnet.ID), URN: types.StringValue(subnet.URN), Name: types.StringValue(subnet.Name),
			VPCID: types.StringValue(subnet.VPCID), CIDR: types.StringValue(subnet.CIDR),
			Status: types.StringValue(subnet.Status), LastError: types.StringValue(subnet.LastError),
			Tags: common.TagsToTerraform(ctx, subnet.Tags, diags),
		})
	}
	result, conversionDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: inlineSubnetTypes}, models)
	diags.Append(conversionDiags...)
	return result
}
