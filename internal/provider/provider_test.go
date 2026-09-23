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
	wantResources := []string{
		"asc_vpc", "asc_subnet", "asc_virtual_machine",
		"asc_vm_group", "asc_volume", "asc_volume_attachment",
		"asc_role", "asc_group", "asc_ip",
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
		"asc_vm_group", "asc_volume", "asc_role", "asc_group", "asc_ips",
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
