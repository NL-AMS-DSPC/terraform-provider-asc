package common

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

// Deprecation messages for arguments ASC dropped; the provider ignores them.
const (
	SubnetTypeDeprecation    = "ASC no longer has subnet types; remove the type argument. It is ignored."
	EnableLoggingDeprecation = "ASC no longer has a logging setting for virtual machines; remove the enable_logging argument. It is ignored."
)

// IgnoredString plans the stored value of an argument ASC no longer has, so removing the
// argument from a configuration, or leaving it out of a new one, plans no change.
func IgnoredString() planmodifier.String { return ignored{} }

// IgnoredBool is IgnoredString for boolean arguments.
func IgnoredBool() planmodifier.Bool { return ignored{} }

type ignored struct{}

func (ignored) Description(context.Context) string {
	return "Keeps the stored value; the argument is ignored."
}

func (i ignored) MarkdownDescription(ctx context.Context) string { return i.Description(ctx) }

func (ignored) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.ConfigValue.IsNull() {
		resp.PlanValue = req.StateValue
	}
}

func (ignored) PlanModifyBool(_ context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.ConfigValue.IsNull() {
		resp.PlanValue = req.StateValue
	}
}
