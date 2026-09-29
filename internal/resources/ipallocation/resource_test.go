package ipallocation

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestImportStateRejectsAnInvalidAddressWithoutWritingState(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"app:10.0.0.01", "app:2001:db8::1", "app:app"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			response := importState(t, id)
			if !response.Diagnostics.HasError() {
				t.Fatalf("ImportState(%q) reported no error", id)
			}
			if !response.State.Raw.IsNull() {
				t.Fatalf("ImportState(%q) wrote state %v, want none", id, response.State.Raw)
			}
		})
	}
}

func TestImportStateSplitsNameAndAddress(t *testing.T) {
	t.Parallel()
	response := importState(t, "app:10.0.0.1")
	if response.Diagnostics.HasError() {
		t.Fatalf("ImportState() diagnostics = %v", response.Diagnostics)
	}
	var model Model
	if diags := response.State.Get(context.Background(), &model); diags.HasError() {
		t.Fatalf("State.Get() diagnostics = %v", diags)
	}
	want := Model{VMName: types.StringValue("app"), IP: types.StringValue("10.0.0.1")}
	if model != want {
		t.Fatalf("imported model = %+v, want %+v", model, want)
	}
}

// importState starts from the null state Terraform hands the provider on import
func importState(t *testing.T, id string) *resource.ImportStateResponse {
	t.Helper()
	ctx := context.Background()
	schemaResponse := &resource.SchemaResponse{}
	(&Resource{}).Schema(ctx, resource.SchemaRequest{}, schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", schemaResponse.Diagnostics)
	}
	response := &resource.ImportStateResponse{State: tfsdk.State{
		Schema: schemaResponse.Schema,
		Raw:    tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil),
	}}
	(&Resource{}).ImportState(ctx, resource.ImportStateRequest{ID: id}, response)
	return response
}
