package common

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// raw gives a plan or state a null or non-null object for the modifier's null checks.
func raw(null bool) tfsdk.Plan {
	object := tftypes.Object{AttributeTypes: map[string]tftypes.Type{}}
	if null {
		return tfsdk.Plan{Raw: tftypes.NewValue(object, nil)}
	}
	return tfsdk.Plan{Raw: tftypes.NewValue(object, map[string]tftypes.Value{})}
}

func TestBootDiskSizePlanModifier(t *testing.T) {
	t.Parallel()
	null, sixtyFour, thirtyTwo := types.Int64Null(), types.Int64Value(64), types.Int64Value(32)

	tests := []struct {
		name        string
		creating    bool
		config      types.Int64
		state       types.Int64
		wantPlan    types.Int64
		wantReplace bool
	}{
		{"create without a value defaults to 32", true, null, null, thirtyTwo, false},
		{"create with a value keeps it", true, sixtyFour, null, sixtyFour, false},
		{"existing state without the attribute and no config plans nothing", false, null, null, null, false},
		{"existing state without the attribute and config 32 does not replace", false, thirtyTwo, null, thirtyTwo, false},
		{"existing state without the attribute and config 64 replaces", false, sixtyFour, null, sixtyFour, true},
		{"existing state 64 and no config keeps 64", false, null, sixtyFour, sixtyFour, false},
		{"existing state 64 and config 32 replaces", false, thirtyTwo, sixtyFour, thirtyTwo, true},
		{"existing state 32 and config 32 plans nothing", false, thirtyTwo, thirtyTwo, thirtyTwo, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := raw(test.creating)
			request := planmodifier.Int64Request{
				Plan:        raw(false),
				State:       tfsdk.State{Raw: state.Raw},
				ConfigValue: test.config,
				PlanValue:   test.config,
				StateValue:  test.state,
			}
			if test.config.IsNull() {
				request.PlanValue = types.Int64Unknown()
			}
			response := &planmodifier.Int64Response{PlanValue: request.PlanValue}
			bootDiskSize{}.PlanModifyInt64(context.Background(), request, response)

			if !response.PlanValue.Equal(test.wantPlan) {
				t.Errorf("plan = %s, want %s", response.PlanValue, test.wantPlan)
			}
			if response.RequiresReplace != test.wantReplace {
				t.Errorf("RequiresReplace = %t, want %t", response.RequiresReplace, test.wantReplace)
			}
		})
	}
}
