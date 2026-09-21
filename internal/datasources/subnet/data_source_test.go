package subnet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

type stubClient struct {
	subnet client.Subnet
	err    error
}

func (s stubClient) GetSubnet(context.Context, string, string) (client.Subnet, error) {
	return s.subnet, s.err
}

func dataSourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&DataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	return response.Schema
}

// read drives the data source the way the framework does: a config value that
// carries only the required attributes, everything else null.
func read(t *testing.T, stub stubClient, vpcName, name string) (Model, datasource.ReadResponse) {
	t.Helper()
	ctx := context.Background()
	dataSchema := dataSourceSchema(t)
	objectType, ok := dataSchema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type = %T, want tftypes.Object", dataSchema.Type().TerraformType(ctx))
	}
	values := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for attribute, attributeType := range objectType.AttributeTypes {
		values[attribute] = tftypes.NewValue(attributeType, nil)
	}
	values["vpc_name"] = tftypes.NewValue(tftypes.String, vpcName)
	values["name"] = tftypes.NewValue(tftypes.String, name)

	response := datasource.ReadResponse{State: tfsdk.State{Schema: dataSchema, Raw: tftypes.NewValue(objectType, nil)}}
	(&DataSource{client: stub}).Read(ctx, datasource.ReadRequest{
		Config: tfsdk.Config{Schema: dataSchema, Raw: tftypes.NewValue(objectType, values)},
	}, &response)

	var state Model
	if !response.Diagnostics.HasError() {
		if diags := response.State.Get(ctx, &state); diags.HasError() {
			t.Fatalf("State.Get() diagnostics = %v", diags)
		}
	}
	return state, response
}

func TestDataSourceIdentity(t *testing.T) {
	t.Parallel()

	var metadata datasource.MetadataResponse
	(&DataSource{}).Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "asc"}, &metadata)
	if metadata.TypeName != "asc_subnet" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
}

func TestDataSourceSchemaMirrorsTheResource(t *testing.T) {
	t.Parallel()

	attributes := dataSourceSchema(t).Attributes
	// A subnet is addressed by the VPC name and its own name, because
	// infra-gateway only serves the VPC's subnet listing. Everything else is
	// served and may never be set in a configuration.
	required := map[string]bool{"name": true, "vpc_name": true}
	for _, attribute := range []string{"id", "urn", "name", "vpc_name", "vpc_id", "cidr", "type", "status", "last_error", "tags"} {
		found, ok := attributes[attribute]
		if !ok {
			t.Fatalf("attribute %q is missing", attribute)
		}
		if found.IsRequired() != required[attribute] || found.IsComputed() == required[attribute] {
			t.Errorf("attribute %q: required = %t, computed = %t", attribute, found.IsRequired(), found.IsComputed())
		}
		if found.IsOptional() {
			t.Errorf("attribute %q is optional, which would let a configuration override a served value", attribute)
		}
	}
	if len(attributes) != 10 {
		t.Fatalf("attributes = %d, want 10", len(attributes))
	}
}

func TestDataSourceReadMapsTheSubnet(t *testing.T) {
	t.Parallel()

	state, response := read(t, stubClient{subnet: client.Subnet{
		ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:test-subnet", Name: "test-subnet",
		VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.128/25", Type: "public", Status: "active",
		Tags: []client.Tag{{Key: "env", Value: "test"}},
	}}, "test-vpc", "test-subnet")
	if response.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", response.Diagnostics)
	}
	if state.ID.ValueString() != "00000000-0000-4000-8000-000000000002" || state.Type.ValueString() != "public" {
		t.Fatalf("state = %#v", state)
	}
	// vpc_name is not part of the read model, so it can only come back from the
	// configuration; losing it would make the state unusable as an address.
	if state.VPCName.ValueString() != "test-vpc" {
		t.Fatalf("vpc_name = %q, want the configured VPC", state.VPCName.ValueString())
	}
}

func TestDataSourceReadReportsAbsentSubnet(t *testing.T) {
	t.Parallel()

	_, response := read(t, stubClient{err: fmt.Errorf("%w: subnet %q in VPC %q", client.ErrResourceNotFound, "ghost", "test-vpc")}, "test-vpc", "ghost")
	if !response.Diagnostics.HasError() {
		t.Fatal("Read() produced no diagnostics for an absent subnet")
	}
	failure := response.Diagnostics.Errors()[0]
	if failure.Summary() != "Subnet not found" {
		t.Fatalf("summary = %q", failure.Summary())
	}
	if !strings.Contains(failure.Detail(), `"ghost"`) || !strings.Contains(failure.Detail(), `"test-vpc"`) {
		t.Fatalf("detail = %q, want both configured names", failure.Detail())
	}
}

func TestDataSourceReadReportsTransportFailure(t *testing.T) {
	t.Parallel()

	_, response := read(t, stubClient{err: errors.New("connection refused")}, "test-vpc", "test-subnet")
	if !response.Diagnostics.HasError() {
		t.Fatal("Read() produced no diagnostics for a transport failure")
	}
	failure := response.Diagnostics.Errors()[0]
	if failure.Summary() != "Unable to read subnet" {
		t.Fatalf("summary = %q", failure.Summary())
	}
	if !strings.Contains(failure.Detail(), "connection refused") {
		t.Fatalf("detail = %q, want the underlying error", failure.Detail())
	}
}
