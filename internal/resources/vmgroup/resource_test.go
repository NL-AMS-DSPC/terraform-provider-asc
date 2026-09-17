package vmgroup

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// replaceDescription is the description every RequiresReplace plan modifier
// reports, whatever the attribute type; matching on it keeps the assertions
// independent of the unexported modifier types.
const replaceDescription = "If the value of this attribute changes, Terraform will destroy and recreate the resource."

type describable interface {
	Description(context.Context) string
}

func requiresReplace[T describable](modifiers []T) bool {
	for _, modifier := range modifiers {
		if modifier.Description(context.Background()) == replaceDescription {
			return true
		}
	}
	return false
}

func groupSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&Resource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	return response.Schema
}

func TestResourceMetadataAndIdentity(t *testing.T) {
	t.Parallel()

	var metadata resource.MetadataResponse
	(&Resource{}).Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "asc"}, &metadata)
	if metadata.TypeName != "asc_vm_group" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}

	attributes := groupSchema(t).Attributes
	// The group is addressed by name everywhere in the gateway, so like
	// asc_virtual_machine it carries no synthetic id attribute.
	if _, exists := attributes["id"]; exists {
		t.Error("schema has an id attribute, want the group addressed by name")
	}
	name, ok := attributes["name"].(schema.StringAttribute)
	if !ok || !name.Required || !requiresReplace(name.PlanModifiers) {
		t.Fatalf("name = %#v, want a required RequiresReplace attribute", attributes["name"])
	}
	if got := len(name.Validators); got != 2 {
		t.Errorf("name validators = %d, want 2", got)
	}
}

func TestResourceReplacesOnEveryConfigurableAttribute(t *testing.T) {
	t.Parallel()

	attributes := groupSchema(t).Attributes
	// infra-gateway has no patch, scale or resize route for a group, so nothing
	// configurable can be updated in place. Tags included: unlike a VPC's, they
	// cannot be reconciled and must force a replacement.
	for _, name := range []string{"name", "sku_id", "vpc_id", "image"} {
		attribute, ok := attributes[name].(schema.StringAttribute)
		if !ok || !requiresReplace(attribute.PlanModifiers) {
			t.Errorf("%s = %#v, want RequiresReplace", name, attributes[name])
		}
	}
	tags, ok := attributes["tags"].(schema.MapAttribute)
	if !ok || !tags.Optional || !requiresReplace(tags.PlanModifiers) {
		t.Errorf("tags = %#v, want an optional RequiresReplace map", attributes["tags"])
	}
	for _, name := range []string{"autoscaling", "customization"} {
		attribute, ok := attributes[name].(schema.SingleNestedAttribute)
		if !ok || !requiresReplace(attribute.PlanModifiers) {
			t.Errorf("%s = %#v, want RequiresReplace", name, attributes[name])
		}
	}
}

func TestResourceKeepsUnreadableAttributesConfigurationOnly(t *testing.T) {
	t.Parallel()

	attributes := groupSchema(t).Attributes
	// The gateway never serves the image back, so marking it Computed would
	// leave an unknown value that no read could ever fill.
	image, ok := attributes["image"].(schema.StringAttribute)
	if !ok || !image.Optional || image.Computed {
		t.Errorf("image = %#v, want optional and not computed", attributes["image"])
	}
	vpcID, ok := attributes["vpc_id"].(schema.StringAttribute)
	if !ok || !vpcID.Required || len(vpcID.Validators) != 1 {
		t.Errorf("vpc_id = %#v, want required with a UUID validator", attributes["vpc_id"])
	}
	customization, ok := attributes["customization"].(schema.SingleNestedAttribute)
	if !ok || !customization.Optional || !customization.Sensitive || customization.Computed {
		t.Errorf("customization = %#v, want an optional sensitive nested attribute", attributes["customization"])
	}
}

func TestResourceComputedReadModel(t *testing.T) {
	t.Parallel()

	attributes := groupSchema(t).Attributes
	for _, name := range []string{"urn", "status", "last_error"} {
		attribute, ok := attributes[name].(schema.StringAttribute)
		if !ok || !attribute.Computed {
			t.Errorf("%s = %#v, want computed", name, attributes[name])
		}
	}
	if volumes, ok := attributes["attached_volumes"].(schema.ListAttribute); !ok || !volumes.Computed {
		t.Errorf("attached_volumes = %#v, want a computed list", attributes["attached_volumes"])
	}
	sku, ok := attributes["sku"].(schema.SingleNestedAttribute)
	if !ok || !sku.Computed {
		t.Fatalf("sku = %#v, want a computed nested object", attributes["sku"])
	}
	if got := len(sku.Attributes); got != 10 {
		t.Errorf("sku attributes = %d, want the 10 asc_virtual_machine reports", got)
	}
}

func TestResourceAutoscalingSchema(t *testing.T) {
	t.Parallel()

	autoscaling, ok := groupSchema(t).Attributes["autoscaling"].(schema.SingleNestedAttribute)
	if !ok || !autoscaling.Required {
		t.Fatalf("autoscaling = %#v, want a required nested object", groupSchema(t).Attributes["autoscaling"])
	}
	minReplicas, ok := autoscaling.Attributes["min_replicas"].(schema.Int64Attribute)
	if !ok || !minReplicas.Required || len(minReplicas.Validators) != 1 {
		t.Errorf("min_replicas = %#v, want required and bounded", autoscaling.Attributes["min_replicas"])
	}
	// max_replicas carries both the gateway's 1..100 bound and the ordering rule
	// that keeps an inverted range from reaching the gateway as a 400.
	maxReplicas, ok := autoscaling.Attributes["max_replicas"].(schema.Int64Attribute)
	if !ok || !maxReplicas.Required || len(maxReplicas.Validators) != 2 {
		t.Errorf("max_replicas = %#v, want required with a bound and a min_replicas comparison", autoscaling.Attributes["max_replicas"])
	}
	cron, ok := autoscaling.Attributes["cron"].(schema.SingleNestedAttribute)
	if !ok || !cron.Optional {
		t.Fatalf("cron = %#v, want an optional nested object", autoscaling.Attributes["cron"])
	}
	// The gateway requires all four cron fields as soon as the object is present.
	for _, name := range []string{"timezone", "start", "end"} {
		attribute, ok := cron.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.Required {
			t.Errorf("cron.%s = %#v, want required", name, cron.Attributes[name])
		}
	}
	if desired, ok := cron.Attributes["desired_replicas"].(schema.Int64Attribute); !ok || !desired.Required {
		t.Errorf("cron.desired_replicas = %#v, want required", cron.Attributes["desired_replicas"])
	}
}
