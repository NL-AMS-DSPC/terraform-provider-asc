package client

import (
	"context"
	"fmt"
	"net/http"
	"sort"
)

// TagPatch is one entry of an infra-gateway tag patch. The gateway applies a
// patch, not a replacement: an entry sets or overwrites its key, and an entry
// with an empty value deletes it. Keys the patch does not mention are left
// untouched, so removing a tag requires an explicit empty-valued entry.
type TagPatch struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type tagPatchRequest struct {
	Tags []TagPatch `json:"tags"`
}

// tagPatchFor computes the patch that turns current into desired, ordered by
// key so the same change always produces the same request body.
//
// An empty desired value is rejected rather than sent: infra-gateway reads it
// as a deletion, so accepting one would silently drop the tag instead of
// storing the empty string the configuration asked for.
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

// sameTags reports whether two tag sets hold the same key/value pairs. Neither
// infra-gateway nor the provider promises an order, so only the pairs matter.
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

// patchTags sends a tag patch to one resource path.
func (c *Runtime) patchTags(ctx context.Context, path string, patch []TagPatch) error {
	return c.request(ctx, http.MethodPatch, path, tagPatchRequest{Tags: patch}, nil)
}

// reconcileTags moves a resource's tags from current to desired and returns the
// resource as the gateway reports it afterwards.
//
// The PATCH response body is deliberately discarded. The virtual machine and
// volume handlers echo the request's own patch entries back as the resource's
// tags, which for a deletion means an entry with an empty value and for any
// patch means the untouched keys are missing; the VPC and subnet handlers do
// re-read, but from a projection that need not have applied the command yet.
// Reading back until the authoritative read reports the desired set is the only
// way to end with state that matches the server.
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
