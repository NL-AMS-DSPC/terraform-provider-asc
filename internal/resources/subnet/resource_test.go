package subnet

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestResourceIdentity(t *testing.T) {
	t.Parallel()

	implementation := &Resource{}
	var metadata resource.MetadataResponse
	implementation.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "asc"}, &metadata)
	if metadata.TypeName != "asc_subnet" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var schemaResponse resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() || len(schemaResponse.Schema.Attributes) != 9 {
		t.Fatalf("schema attributes = %d, diagnostics = %v", len(schemaResponse.Schema.Attributes), schemaResponse.Diagnostics)
	}
}
