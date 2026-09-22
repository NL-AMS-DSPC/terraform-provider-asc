package client

import (
	"context"
	"fmt"
	"sort"
)

// TagPatch sets a key, or deletes it when Value is empty. Omitted keys are unchanged.
type TagPatch struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type tagPatchRequest struct {
	Tags []TagPatch `json:"tags"`
}

// tagPatchFor returns a deterministic patch. Empty desired values are rejected because the
// gateway interprets them as deletions.
func tagPatchFor(current, desired []Tag) ([]TagPatch, error) {
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

	patch := make([]TagPatch, 0, len(desired))
	for key, value := range desiredByKey {
		if existing, ok := currentByKey[key]; !ok || existing != value {
			patch = append(patch, TagPatch{Key: key, Value: value})
		}
	}
	for key := range currentByKey {
		if _, ok := desiredByKey[key]; !ok {
			patch = append(patch, TagPatch{Key: key})
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

func (c *Runtime) patchTags(ctx context.Context, path string, patch []TagPatch) error {
	return c.patch(ctx, path, tagPatchRequest{Tags: patch})
}

// reconcileTags polls until a read reports the desired tags. PATCH responses may echo only
// the patch or return a stale projection.
func reconcileTags[T resourceView](ctx context.Context, c *Runtime, kind, name, path string, current, desired []Tag, read readFunc[T]) (T, error) {
	var zero T
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	patch, err := tagPatchFor(current, desired)
	if err != nil {
		return zero, fmt.Errorf("update %s %q tags: %w", kind, name, err)
	}
	if len(patch) > 0 {
		if err := c.patchTags(operationCtx, path, patch); err != nil {
			return zero, fmt.Errorf("update %s %q tags: %w", kind, name, err)
		}
	}

	return waitUntil(operationCtx, c, kind, name, read, func(current T) bool {
		return sameTags(current.tags(), desired)
	}, "report its updated tags")
}
