package vpc

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
	vpc client.VPC
	err error
}

func (s stubClient) GetVPC(context.Context, string) (client.VPC, error) { return s.vpc, s.err }

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
// carries only the required attribute, everything else null.
func read(t *testing.T, stub stubClient, name string) (Model, datasource.ReadResponse) {
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
	if metadata.TypeName != "asc_vpc" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
}

func TestDataSourceSchemaMirrorsTheResource(t *testing.T) {
	t.Parallel()

	attributes := dataSourceSchema(t).Attributes
	// name identifies the VPC to look up; everything else is served by
	// infra-gateway and may never be set in a configuration.
	required := map[string]bool{"name": true}
	for _, attribute := range []string{"id", "urn", "name", "cidr", "status", "last_error", "tags", "subnets"} {
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
	if len(attributes) != 8 {
		t.Fatalf("attributes = %d, want 8", len(attributes))
	}

	nested, ok := attributes["subnets"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("subnets = %T, want schema.ListNestedAttribute", attributes["subnets"])
	}
	for _, attribute := range []string{"id", "urn", "name", "vpc_id", "cidr", "type", "status", "last_error", "tags"} {
		found, ok := nested.NestedObject.Attributes[attribute]
		if !ok {
			t.Fatalf("subnets.%s is missing", attribute)
		}
		if !found.IsComputed() {
			t.Errorf("subnets.%s is not computed", attribute)
		}
	}
	if len(nested.NestedObject.Attributes) != 9 {
		t.Fatalf("subnets attributes = %d, want 9", len(nested.NestedObject.Attributes))
	}
}

func TestDataSourceReadMapsTheVPC(t *testing.T) {
	t.Parallel()

	state, response := read(t, stubClient{vpc: client.VPC{
		ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
		CIDR: "10.0.0.0/24", Status: "active", Tags: []client.Tag{{Key: "env", Value: "test"}},
		Subnets: []client.Subnet{{
			ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
			VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.0/25", Status: "active",
		}},
	}}, "test-vpc")
	if response.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", response.Diagnostics)
	}
	if state.ID.ValueString() != "00000000-0000-4000-8000-000000000001" || state.CIDR.ValueString() != "10.0.0.0/24" {
		t.Fatalf("state = %#v", state)
	}
	if length := len(state.Subnets.Elements()); length != 1 {
		t.Fatalf("subnets = %d, want 1", length)
	}
}

func TestDataSourceReadReportsAbsentVPC(t *testing.T) {
	t.Parallel()

	_, response := read(t, stubClient{err: fmt.Errorf("get VPC: %w", client.ErrResourceNotFound)}, "ghost")
	if !response.Diagnostics.HasError() {
		t.Fatal("Read() produced no diagnostics for an absent VPC")
	}
	summary := response.Diagnostics.Errors()[0].Summary()
	if summary != "VPC not found" {
		t.Fatalf("summary = %q", summary)
	}
	if detail := response.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, `"ghost"`) {
		t.Fatalf("detail = %q, want the configured name", detail)
	}
}

func TestDataSourceReadReportsTransportFailure(t *testing.T) {
	t.Parallel()

	_, response := read(t, stubClient{err: errors.New("connection refused")}, "test-vpc")
	if !response.Diagnostics.HasError() {
		t.Fatal("Read() produced no diagnostics for a transport failure")
	}
	failure := response.Diagnostics.Errors()[0]
	if failure.Summary() != "Unable to read VPC" {
		t.Fatalf("summary = %q", failure.Summary())
	}
	if !strings.Contains(failure.Detail(), "connection refused") {
		t.Fatalf("detail = %q, want the underlying error", failure.Detail())
	}
}
