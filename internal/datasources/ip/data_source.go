// Package ip implements the asc_ips data source.
package ip

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

// DataSource lists every IP address currently reserved through IPAM.
type DataSource struct {
	client *client.Runtime
}

// Model mirrors one entry of the asc_ip resource.
type Model struct {
	IP          types.String `tfsdk:"ip"`
	ReservedAt  types.String `tfsdk:"reserved_at"`
	AllocatedTo types.String `tfsdk:"allocated_to"`
}

// DataSourceModel is the top level state of the data source.
type DataSourceModel struct {
	IPs []Model `tfsdk:"ips"`
}

// NewDataSource creates the asc_ips data source.
func NewDataSource() datasource.DataSource { return &DataSource{} }

func (d *DataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ips"
}

func (d *DataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the IP addresses currently reserved for a tenant.",
		Attributes: map[string]schema.Attribute{
			"ips": schema.ListNestedAttribute{
				Description: "Reserved IP addresses, sorted by address so an entry keeps its index when ASC returns them in another order.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"ip":          schema.StringAttribute{Description: "The IP address.", Computed: true},
						"reserved_at": schema.StringAttribute{Description: "Time at which the address was reserved.", Computed: true},
						"allocated_to": schema.StringAttribute{
							Description: "URN of the workload the address is allocated to, null while it is free. Compare it e.g. with asc_virtual_machine.urn.",
							Computed:    true,
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

func (d *DataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	reservations, err := d.client.ListReservations(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list IP reservations", err.Error())
		return
	}

	state := DataSourceModel{IPs: make([]Model, len(reservations))}
	for i, reservation := range reservations {
		state.IPs[i] = Model{
			IP:          types.StringValue(reservation.IP),
			ReservedAt:  types.StringValue(reservation.ReservedAt),
			AllocatedTo: types.StringPointerValue(reservation.AllocatedTo),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
