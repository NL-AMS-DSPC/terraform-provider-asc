package virtualmachine

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func TestResourceIdentityAndSensitiveSchema(t *testing.T) {
	t.Parallel()

	implementation := &Resource{}
	var metadata resource.MetadataResponse
	implementation.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "asc"}, &metadata)
	if metadata.TypeName != "asc_virtual_machine" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var schemaResponse resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", schemaResponse.Diagnostics)
	}
	customization, ok := schemaResponse.Schema.Attributes["customization"].(schema.SingleNestedAttribute)
	if !ok || !customization.Sensitive {
		t.Fatalf("customization = %#v, want sensitive nested attribute", schemaResponse.Schema.Attributes["customization"])
	}
	if got := len(schemaResponse.Schema.Attributes["name"].(schema.StringAttribute).Validators); got != 2 {
		t.Fatalf("name validators = %d, want 2", got)
	}
}
