package virtualmachine

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

func TestVNICsAreRequiredAndReplaceOnlyAKnownList(t *testing.T) {
	t.Parallel()

	var schemaResponse resource.SchemaResponse
	(&Resource{}).Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	vnics, ok := schemaResponse.Schema.Attributes["vnics"].(schema.ListAttribute)
	if !ok || !vnics.Required || len(vnics.PlanModifiers) != 1 {
		t.Fatalf("vnics = %#v, want a required list with one plan modifier", schemaResponse.Schema.Attributes["vnics"])
	}
	if vpcID := schemaResponse.Schema.Attributes["vpc_id"]; !vpcID.IsComputed() || vpcID.IsRequired() || vpcID.IsOptional() {
		t.Fatalf("vpc_id = %#v, want computed only", vpcID)
	}

	known, _ := types.ListValueFrom(context.Background(), types.StringType, []string{"urn:a"})
	for name, test := range map[string]struct {
		state types.List
		want  bool
	}{
		// State from an import or an earlier version has no list yet.
		"null state":  {types.ListNull(types.StringType), false},
		"known state": {known, true},
	} {
		var response listplanmodifier.RequiresReplaceIfFuncResponse
		replaceKnownVNICs(context.Background(), planmodifier.ListRequest{StateValue: test.state}, &response)
		if response.RequiresReplace != test.want {
			t.Errorf("%s: RequiresReplace = %t, want %t", name, response.RequiresReplace, test.want)
		}
	}
}
