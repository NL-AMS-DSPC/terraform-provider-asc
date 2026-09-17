package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
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
	if resources := implementation.Resources(context.Background()); len(resources) != 3 {
		t.Fatalf("resources = %d, want exactly 3", len(resources))
	}
	if dataSources := implementation.DataSources(context.Background()); len(dataSources) != 0 {
		t.Fatalf("data sources = %d, want 0", len(dataSources))
	}
}
