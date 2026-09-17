package vpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	encoded, err := json.Marshal(requests[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(encoded) != `{"name":"bootstrap","cidr":"10.0.0.0/25","type":"private"}` {
		t.Fatalf("payload = %s", encoded)
	}
}
