package virtualmachine

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
	vm  client.VM
	err error
}

func (s stubClient) GetVM(context.Context, string) (client.VM, error) { return s.vm, s.err }

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
	if metadata.TypeName != "asc_virtual_machine" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
}

func TestDataSourceSchemaMirrorsTheReadableResource(t *testing.T) {
	t.Parallel()

	attributes := dataSourceSchema(t).Attributes
	// name identifies the machine to look up; everything else is served by
	// infra-gateway and may never be set in a configuration.
	required := map[string]bool{"name": true}
	for _, attribute := range []string{"urn", "name", "sku", "vpc_id", "status", "last_error", "tags", "attached_volumes", "os"} {
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
	if len(attributes) != 9 {
		t.Fatalf("attributes = %d, want 9", len(attributes))
	}
	// image and sku_id would only repeat os.id and sku.id, and customization is
	// write-only, so none of them belong on a read-only view.
	for _, attribute := range []string{"image", "sku_id", "customization"} {
		if _, ok := attributes[attribute]; ok {
			t.Errorf("attribute %q must not be exposed by the data source", attribute)
		}
	}

	sku, ok := attributes["sku"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("sku = %T, want schema.SingleNestedAttribute", attributes["sku"])
	}
	for _, attribute := range []string{"id", "name", "family", "threads", "cores", "memory_in_mb", "storage_in_gb", "storage_type", "gpu_count", "gpu_type"} {
		found, ok := sku.Attributes[attribute]
		if !ok {
			t.Fatalf("sku.%s is missing", attribute)
		}
		if !found.IsComputed() {
			t.Errorf("sku.%s is not computed", attribute)
		}
	}
	if len(sku.Attributes) != 10 {
		t.Fatalf("sku attributes = %d, want 10", len(sku.Attributes))
	}

	osAttribute, ok := attributes["os"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("os = %T, want schema.SingleNestedAttribute", attributes["os"])
	}
	for _, attribute := range []string{"id", "family", "distribution", "release", "display_name"} {
		found, ok := osAttribute.Attributes[attribute]
		if !ok {
			t.Fatalf("os.%s is missing", attribute)
		}
		if !found.IsComputed() {
			t.Errorf("os.%s is not computed", attribute)
		}
	}
	if len(osAttribute.Attributes) != 5 {
		t.Fatalf("os attributes = %d, want 5", len(osAttribute.Attributes))
	}
}

func TestDataSourceReadMapsTheVirtualMachine(t *testing.T) {
	t.Parallel()

	state, response := read(t, stubClient{vm: client.VM{
		URN: "asc:vm:test-vm", Name: "test-vm", VPCID: "00000000-0000-4000-8000-000000000001",
		SKU:  client.SKU{ID: "gp-2", Name: "general-purpose", Cores: 4, MemoryInMB: 8192},
		OS:   client.OS{ID: "ubuntu-24.04", Family: "linux", Distribution: "ubuntu", Release: "24.04", DisplayName: "Ubuntu 24.04"},
		Tags: []client.Tag{{Key: "env", Value: "test"}}, Status: "active",
	}}, "test-vm")
	if response.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", response.Diagnostics)
	}
	if state.URN.ValueString() != "asc:vm:test-vm" {
		t.Fatalf("state = %#v", state)
	}
	if state.SKU.IsNull() || state.OS.IsNull() {
		t.Fatalf("sku = %v, os = %v, want both populated", state.SKU, state.OS)
	}
}

func TestDataSourceReadSortsAttachedVolumes(t *testing.T) {
	t.Parallel()

	shuffled := []string{"vol-c", "vol-a", "vol-b"}
	state, response := read(t, stubClient{vm: client.VM{
		URN: "asc:vm:test-vm", Name: "test-vm", AttachedVolumes: shuffled,
	}}, "test-vm")
	if response.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", response.Diagnostics)
	}

	var volumes []string
	if diags := state.AttachedVolumes.ElementsAs(context.Background(), &volumes, false); diags.HasError() {
		t.Fatalf("ElementsAs() diagnostics = %v", diags)
	}
	if strings.Join(volumes, ",") != "vol-a,vol-b,vol-c" {
		t.Fatalf("attached_volumes = %v, want sorted", volumes)
	}
	// Sorting in place would reorder the caller's slice; the client owns that
	// value and the read must not mutate it.
	if strings.Join(shuffled, ",") != "vol-c,vol-a,vol-b" {
		t.Fatalf("the client slice was mutated: %v", shuffled)
	}
}

func TestDataSourceReadReportsAbsentVirtualMachine(t *testing.T) {
	t.Parallel()

	_, response := read(t, stubClient{err: fmt.Errorf("get VM: %w", client.ErrResourceNotFound)}, "ghost")
	if !response.Diagnostics.HasError() {
		t.Fatal("Read() produced no diagnostics for an absent virtual machine")
	}
	failure := response.Diagnostics.Errors()[0]
	if failure.Summary() != "Virtual machine not found" {
		t.Fatalf("summary = %q", failure.Summary())
	}
	if !strings.Contains(failure.Detail(), `"ghost"`) {
		t.Fatalf("detail = %q, want the configured name", failure.Detail())
	}
}

func TestDataSourceReadReportsTransportFailure(t *testing.T) {
	t.Parallel()

	_, response := read(t, stubClient{err: errors.New("connection refused")}, "test-vm")
	if !response.Diagnostics.HasError() {
		t.Fatal("Read() produced no diagnostics for a transport failure")
	}
	failure := response.Diagnostics.Errors()[0]
	if failure.Summary() != "Unable to read virtual machine" {
		t.Fatalf("summary = %q", failure.Summary())
	}
	if !strings.Contains(failure.Detail(), "connection refused") {
		t.Fatalf("detail = %q, want the underlying error", failure.Detail())
	}
}
