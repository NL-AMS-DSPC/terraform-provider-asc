package provider

import (
	"context"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestProviderMetadataAndSchema(t *testing.T) {
	t.Parallel()

	implementation := &ASCProvider{version: "test"}
	var metadata provider.MetadataResponse
	implementation.Metadata(context.Background(), provider.MetadataRequest{}, &metadata)
	if metadata.TypeName != "asc" || metadata.Version != "test" {
		t.Fatalf("metadata = %#v", metadata)
	}

	var response provider.SchemaResponse
	implementation.Schema(context.Background(), provider.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	if got, want := len(response.Schema.Attributes), 7; got != want {
		t.Fatalf("attribute count = %d, want %d", got, want)
	}
	password, ok := response.Schema.Attributes["password"].(schema.StringAttribute)
	if !ok || !password.Sensitive {
		t.Fatalf("password attribute = %#v, want sensitive string", response.Schema.Attributes["password"])
	}
	// The registered set is asserted by name, not just by count, so a resource
	// can neither go missing nor appear without this test being updated. Load
	// balancers are deliberately absent: infra-gateway serves no read route for
	// them, so no truthful resource is possible.
	wantResources := []string{
		"asc_vpc", "asc_subnet", "asc_virtual_machine",
		"asc_vm_group", "asc_volume", "asc_volume_attachment",
		"asc_role", "asc_group",
	}
	var gotResources []string
	for _, factory := range implementation.Resources(context.Background()) {
		var metadata resource.MetadataResponse
		factory().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "asc"}, &metadata)
		gotResources = append(gotResources, metadata.TypeName)
	}
	if !slices.Equal(gotResources, wantResources) {
		t.Fatalf("resources = %v, want %v", gotResources, wantResources)
	}

	wantDataSources := []string{
		"asc_skus", "asc_vpc", "asc_subnet", "asc_virtual_machine",
		"asc_vm_group", "asc_volume", "asc_role", "asc_group",
	}
	var gotDataSources []string
	for _, factory := range implementation.DataSources(context.Background()) {
		var metadata datasource.MetadataResponse
		factory().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "asc"}, &metadata)
		gotDataSources = append(gotDataSources, metadata.TypeName)
	}
	if !slices.Equal(gotDataSources, wantDataSources) {
		t.Fatalf("data sources = %v, want %v", gotDataSources, wantDataSources)
	}
}
