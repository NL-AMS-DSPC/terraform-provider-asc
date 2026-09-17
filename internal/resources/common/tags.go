package common

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

func TagsToClient(ctx context.Context, value types.Map, diags *diag.Diagnostics) []client.Tag {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}

	var values map[string]string
	diags.Append(value.ElementsAs(ctx, &values, false)...)
	if diags.HasError() {
		return nil
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]client.Tag, 0, len(keys))
	for _, key := range keys {
		result = append(result, client.Tag{Key: key, Value: values[key]})
	}
	return result
}

func TagsToTerraform(ctx context.Context, values []client.Tag, diags *diag.Diagnostics) types.Map {
	if len(values) == 0 {
		return types.MapNull(types.StringType)
	}

	result := make(map[string]string, len(values))
	for _, tag := range values {
		result[tag.Key] = tag.Value
	}
	value, conversionDiags := types.MapValueFrom(ctx, types.StringType, result)
	diags.Append(conversionDiags...)
	return value
}
