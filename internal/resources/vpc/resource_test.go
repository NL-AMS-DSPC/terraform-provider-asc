package vpc

import (
	"context"

	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

func TestInlineSubnetRequestOmitsParentID(t *testing.T) {
	t.Parallel()

	input, conversionDiags := types.ListValueFrom(context.Background(), types.ObjectType{AttrTypes: inlineSubnetTypes}, []inlineSubnetModel{{
		ID: types.StringUnknown(), URN: types.StringUnknown(), Name: types.StringValue("bootstrap"),
		VPCID: types.StringUnknown(), CIDR: types.StringValue("10.0.0.0/25"), Type: types.StringValue("private"),
		Status: types.StringUnknown(), LastError: types.StringUnknown(), Tags: types.MapNull(types.StringType),
	}})
	if conversionDiags.HasError() {
		t.Fatalf("ListValueFrom() diagnostics = %v", conversionDiags)
	}
	var diags diag.Diagnostics
	requests := inlineSubnetsToClient(context.Background(), input, &diags)
	if diags.HasError() || len(requests) != 1 {
		t.Fatalf("inlineSubnetsToClient() = %#v, diagnostics = %v", requests, diags)
	}
	// The client leaves an empty VPCID out of the request body; see
	// TestInfraRequestBodiesMatchTheHandwrittenWire in internal/client.
	want := client.CreateSubnetRequest{Name: "bootstrap", CIDR: "10.0.0.0/25"}
	if got := requests[0]; got.Name != want.Name || got.CIDR != want.CIDR || got.VPCID != "" || len(got.Tags) != 0 {
		t.Fatalf("request = %#v, want %#v", got, want)
	}
}
