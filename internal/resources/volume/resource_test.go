package volume

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestSchemaAttributes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := testSchema(t)

	if _, exists := volumeSchema.Attributes["id"]; exists {
		t.Error("schema declares id, but the infra-gateway volume read model has none")
	}

	for _, test := range []struct {
		name     string
		required bool
		computed bool
	}{
		{name: "name", required: true},
		{name: "size", required: true},
		{name: "size_bytes", computed: true},
		{name: "storage_class", required: true},
		{name: "access_mode", required: true},
		{name: "volume_mode", computed: true},
		{name: "urn", computed: true},
		{name: "status", computed: true},
		{name: "last_error", computed: true},
		{name: "attached_to_vm", computed: true},
		{name: "labels", computed: true},
		{name: "annotations", computed: true},
	} {
		attribute, exists := volumeSchema.Attributes[test.name]
		if !exists {
			t.Errorf("attribute %q is missing", test.name)
			continue
		}
		if attribute.IsRequired() != test.required || attribute.IsComputed() != test.computed {
			t.Errorf("attribute %q required = %t, computed = %t; want required = %t, computed = %t",
				test.name, attribute.IsRequired(), attribute.IsComputed(), test.required, test.computed)
		}
	}

	tags, exists := volumeSchema.Attributes["tags"].(schema.MapAttribute)
	if !exists {
		t.Fatal("tags is not a map attribute")
	}
	if !tags.IsOptional() || tags.IsRequired() || tags.IsComputed() {
		t.Errorf("tags optional = %t, required = %t, computed = %t; want optional only", tags.IsOptional(), tags.IsRequired(), tags.IsComputed())
	}
	// Tags are patched in place, so they must not force a replacement the way
	// they do on the resources whose gateway handlers cannot update them.
	if len(tags.PlanModifiers) != 0 {
		t.Errorf("tags plan modifiers = %d, want none: tags are updated in place", len(tags.PlanModifiers))
	}

	for name, wantReplace := range map[string]bool{
		"name": true, "storage_class": true, "access_mode": true,
		// A resize is an in-place update, so size must never force replacement.
		"size": false,
	} {
		if got := stringRequiresReplace(ctx, t, volumeSchema, name); got != wantReplace {
			t.Errorf("attribute %q requires replacement = %t, want %t", name, got, wantReplace)
		}
	}

	accessMode, exists := volumeSchema.Attributes["access_mode"].(schema.StringAttribute)
	if !exists {
		t.Fatal("access_mode is not a string attribute")
	}
	for _, value := range []string{"ReadWriteOnce", "ReadWriteMany", "ReadOnlyMany"} {
		if diags := validateString(ctx, accessMode.Validators, value); diags.HasError() {
			t.Errorf("access_mode %q rejected: %v", value, diags)
		}
	}
	if diags := validateString(ctx, accessMode.Validators, "ReadWriteEveryone"); !diags.HasError() {
		t.Error("access_mode accepted a value infra-gateway does not allow")
	}
}

func TestModifyPlanRejectsShrinkAtPlanTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := testSchema(t)

	state := testState(ctx, t, volumeSchema, testModel("10Gi", 10<<30))
	plan := testPlan(ctx, t, volumeSchema, testModel("5Gi", 10<<30))
	response := &resource.ModifyPlanResponse{Plan: plan}
	(&Resource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, response)

	if !response.Diagnostics.HasError() {
		t.Fatal("ModifyPlan() accepted a shrink")
	}
	detail := response.Diagnostics.Errors()[0].Detail()
	for _, fragment := range []string{"10Gi", "5Gi", "10737418240", "5368709120"} {
		if !strings.Contains(detail, fragment) {
			t.Errorf("shrink diagnostic %q does not name %q", detail, fragment)
		}
	}
}

func TestModifyPlanAcceptsEqualSizeSpelledDifferently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := testSchema(t)

	state := testState(ctx, t, volumeSchema, testModel("10Gi", 10<<30))
	plan := testPlan(ctx, t, volumeSchema, testModel("10240Mi", 10<<30))
	response := &resource.ModifyPlanResponse{Plan: plan}
	(&Resource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, response)

	if response.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() diagnostics = %v", response.Diagnostics)
	}
}

func TestModifyPlanDropsComputedValuesAnInPlaceChangeInvalidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := testSchema(t)

	state := testState(ctx, t, volumeSchema, testModel("10Gi", 10<<30))
	plan := testPlan(ctx, t, volumeSchema, testModel("20Gi", 10<<30))
	response := &resource.ModifyPlanResponse{Plan: plan}
	(&Resource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, response)

	if response.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() diagnostics = %v", response.Diagnostics)
	}
	var planned Model
	if diags := response.Plan.Get(ctx, &planned); diags.HasError() {
		t.Fatalf("Plan.Get() diagnostics = %v", diags)
	}
	if !planned.SizeBytes.IsUnknown() || !planned.Status.IsUnknown() || !planned.Labels.IsUnknown() {
		t.Fatalf("planned size_bytes = %v, status = %v, labels = %v; all must be unknown after a resize",
			planned.SizeBytes, planned.Status, planned.Labels)
	}
}

func TestModifyPlanLeavesAnUnchangedPlanAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := testSchema(t)

	state := testState(ctx, t, volumeSchema, testModel("10Gi", 10<<30))
	plan := testPlan(ctx, t, volumeSchema, testModel("10Gi", 10<<30))
	response := &resource.ModifyPlanResponse{Plan: plan}
	(&Resource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, response)

	var planned Model
	if diags := response.Plan.Get(ctx, &planned); diags.HasError() {
		t.Fatalf("Plan.Get() diagnostics = %v", diags)
	}
	if planned.SizeBytes.IsUnknown() {
		t.Fatal("ModifyPlan() made size_bytes unknown without a change, which would never let a plan settle")
	}
}

func TestModifyPlanIgnoresCreateAndDestroy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := testSchema(t)
	nullRaw := tftypes.NewValue(volumeSchema.Type().TerraformType(ctx), nil)

	plan := testPlan(ctx, t, volumeSchema, testModel("5Gi", 5<<30))
	response := &resource.ModifyPlanResponse{Plan: plan}
	(&Resource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: tfsdk.State{Schema: volumeSchema, Raw: nullRaw}, Plan: plan,
	}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() on a create diagnostics = %v", response.Diagnostics)
	}

	state := testState(ctx, t, volumeSchema, testModel("10Gi", 10<<30))
	destroyPlan := tfsdk.Plan{Schema: volumeSchema, Raw: nullRaw}
	response = &resource.ModifyPlanResponse{Plan: destroyPlan}
	(&Resource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: destroyPlan}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan() on a destroy diagnostics = %v", response.Diagnostics)
	}
}

func testSchema(t *testing.T) schema.Schema {
	t.Helper()
	response := &resource.SchemaResponse{}
	(&Resource{}).Schema(context.Background(), resource.SchemaRequest{}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	return response.Schema
}

func testModel(size string, sizeBytes int64) Model {
	return Model{
		Name: types.StringValue("data"), Size: types.StringValue(size), SizeBytes: types.Int64Value(sizeBytes),
		StorageClass: types.StringValue("standard"), AccessMode: types.StringValue("ReadWriteOnce"),
		VolumeMode: types.StringValue("Filesystem"), URN: types.StringValue("asc:block_volume:data"),
		Status: types.StringValue("detached"), LastError: types.StringValue(""), AttachedToVM: types.StringValue(""),
		Tags: types.MapNull(types.StringType), Labels: types.MapNull(types.StringType), Annotations: types.MapNull(types.StringType),
	}
}

func testState(ctx context.Context, t *testing.T, volumeSchema schema.Schema, model Model) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: volumeSchema}
	if diags := state.Set(ctx, model); diags.HasError() {
		t.Fatalf("State.Set() diagnostics = %v", diags)
	}
	return state
}

func testPlan(ctx context.Context, t *testing.T, volumeSchema schema.Schema, model Model) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: volumeSchema}
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatalf("Plan.Set() diagnostics = %v", diags)
	}
	return plan
}

// stringRequiresReplace drives the attribute's plan modifiers with a changed
// value, which is the only situation in which RequiresReplace reports itself.
func stringRequiresReplace(ctx context.Context, t *testing.T, volumeSchema schema.Schema, name string) bool {
	t.Helper()
	attribute, ok := volumeSchema.Attributes[name].(schema.StringAttribute)
	if !ok {
		t.Fatalf("attribute %q is not a string attribute", name)
	}
	state := testState(ctx, t, volumeSchema, testModel("10Gi", 10<<30))
	plan := testPlan(ctx, t, volumeSchema, testModel("20Gi", 10<<30))

	for _, modifier := range attribute.PlanModifiers {
		response := &planmodifier.StringResponse{}
		modifier.PlanModifyString(ctx, planmodifier.StringRequest{
			Path: path.Root(name), State: state, Plan: plan,
			StateValue: types.StringValue("original"), PlanValue: types.StringValue("changed"), ConfigValue: types.StringValue("changed"),
		}, response)
		if response.RequiresReplace {
			return true
		}
	}
	return false
}

func validateString(ctx context.Context, validators []validator.String, value string) diag.Diagnostics {
	response := &validator.StringResponse{}
	for _, candidate := range validators {
		candidate.ValidateString(ctx, validator.StringRequest{Path: path.Root("access_mode"), ConfigValue: types.StringValue(value)}, response)
	}
	return response.Diagnostics
}
