package client

import (
	"context"
	"fmt"
	"sort"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
)

// tagPatchFor returns a deterministic patch. An entry sets its key, or deletes it when its
// value is empty; omitted keys are unchanged. Every entry carries a value, so a deletion
// travels as an explicit empty string. Empty desired values are rejected because the
// gateway interprets them as deletions.
func tagPatchFor(current, desired []Tag) ([]infra.PatchTag, error) {
	desiredByKey := make(map[string]string, len(desired))
	for _, tag := range desired {
		if tag.Value == "" {
			return nil, fmt.Errorf("tag %q has an empty value: infra-gateway reads an empty tag value as a deletion, so tag values must not be empty", tag.Key)
		}
		desiredByKey[tag.Key] = tag.Value
	}

	currentByKey := make(map[string]string, len(current))
	for _, tag := range current {
		currentByKey[tag.Key] = tag.Value
	}

	patch := make([]infra.PatchTag, 0, len(desired))
	for key, value := range desiredByKey {
		if existing, ok := currentByKey[key]; !ok || existing != value {
			patch = append(patch, infra.PatchTag{Key: key, Value: &value})
		}
	}
	for key := range currentByKey {
		if _, ok := desiredByKey[key]; !ok {
			deletion := ""
			patch = append(patch, infra.PatchTag{Key: key, Value: &deletion})
		}
	}
	sort.Slice(patch, func(i, j int) bool { return patch[i].Key < patch[j].Key })
	return patch, nil
}

// sameTags compares key/value pairs regardless of order.
func sameTags(left, right []Tag) bool {
	if len(left) != len(right) {
		return false
	}
	byKey := make(map[string]string, len(left))
	for _, tag := range left {
		byKey[tag.Key] = tag.Value
	}
	for _, tag := range right {
		if value, ok := byKey[tag.Key]; !ok || value != tag.Value {
			return false
		}
	}
	return true
}

// tagPatcher sends a tag patch to one resource's PATCH operation.
type tagPatcher func(ctx context.Context, body infra.PatchTagsRequest) error

// reconcileTags polls until a read reports the desired tags. PATCH responses may echo only
// the patch or return a stale projection.
func reconcileTags[T resourceView](ctx context.Context, c *Runtime, kind, name string, send tagPatcher, current, desired []Tag, read readFunc[T]) (T, error) {
	var zero T
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	patch, err := tagPatchFor(current, desired)
	if err != nil {
		return zero, fmt.Errorf("update %s %q tags: %w", kind, name, err)
	}
	if len(patch) > 0 {
		if err := send(operationCtx, infra.PatchTagsRequest{Tags: &patch}); err != nil {
			return zero, fmt.Errorf("update %s %q tags: %w", kind, name, err)
		}
	}

	return waitUntil(operationCtx, c, kind, name, read, func(current T) bool {
		return sameTags(current.tags(), desired)
	}, "report its updated tags")
}
