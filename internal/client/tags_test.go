package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTagPatchForComputesSetsAndDeletes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		current []Tag
		desired []Tag
		want    []TagPatch
	}{
		{
			name:    "adds a new key",
			current: []Tag{{Key: "env", Value: "test"}},
			desired: []Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}},
			want:    []TagPatch{{Key: "owner", Value: "platform"}},
		},
		{
			name:    "overwrites a changed value",
			current: []Tag{{Key: "env", Value: "test"}},
			desired: []Tag{{Key: "env", Value: "prod"}},
			want:    []TagPatch{{Key: "env", Value: "prod"}},
		},
		{
			name:    "deletes a removed key with an empty value",
			current: []Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}},
			desired: []Tag{{Key: "env", Value: "test"}},
			want:    []TagPatch{{Key: "owner"}},
		},
		{
			name:    "clears every tag",
			current: []Tag{{Key: "b", Value: "2"}, {Key: "a", Value: "1"}},
			desired: nil,
			want:    []TagPatch{{Key: "a"}, {Key: "b"}},
		},
		{
			name:    "is empty when nothing changed",
			current: []Tag{{Key: "env", Value: "test"}},
			desired: []Tag{{Key: "env", Value: "test"}},
			want:    []TagPatch{},
		},
		{
			name:    "orders mixed changes by key",
			current: []Tag{{Key: "gone", Value: "1"}, {Key: "kept", Value: "1"}},
			desired: []Tag{{Key: "added", Value: "1"}, {Key: "kept", Value: "2"}},
			want:    []TagPatch{{Key: "added", Value: "1"}, {Key: "gone"}, {Key: "kept", Value: "2"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := tagPatchFor(test.current, test.desired)
			if err != nil {
				t.Fatalf("tagPatchFor() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("tagPatchFor() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestTagPatchForRejectsAnEmptyDesiredValue(t *testing.T) {
	t.Parallel()
	_, err := tagPatchFor(nil, []Tag{{Key: "env", Value: ""}})
	if err == nil || !strings.Contains(err.Error(), "empty tag value") {
		t.Fatalf("tagPatchFor() error = %v, want the empty-value rejection", err)
	}
}

func TestSameTagsIgnoresOrder(t *testing.T) {
	t.Parallel()
	left := []Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	if !sameTags(left, []Tag{{Key: "b", Value: "2"}, {Key: "a", Value: "1"}}) {
		t.Fatal("sameTags() = false, want true for a reordered set")
	}
	if sameTags(left, []Tag{{Key: "a", Value: "1"}}) {
		t.Fatal("sameTags() = true, want false for a smaller set")
	}
	if sameTags(left, []Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "changed"}}) {
		t.Fatal("sameTags() = true, want false for a changed value")
	}
}

// TestReconcileTagsIgnoresThePatchEchoAndReadsBack pins the behaviour the
// virtual machine and volume handlers force: their PATCH response reports only
// the patched keys, so state has to come from the authoritative read.
func TestReconcileTagsIgnoresThePatchEchoAndReadsBack(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var body tagPatchRequest
	patched := false
	reads := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPatch {
			_ = json.NewDecoder(r.Body).Decode(&body)
			patched = true
			// The gateway echoes only the patch entries, including the
			// empty-valued deletion. State must not be taken from here.
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VPC{Name: "test-vpc", Status: "active", Tags: []Tag{{Key: "env", Value: "prod"}, {Key: "owner"}}}})
			return
		}
		reads++
		current := VPC{Name: "test-vpc", Status: "active", Tags: []Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}}}
		if patched && reads > 1 {
			current.Tags = []Tag{{Key: "env", Value: "prod"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": current})
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	runtime := NewRuntime(config)
	runtime.pollInterval = time.Millisecond
	runtime.lifecycleTimeout = time.Second

	read := func(ctx context.Context) (VPC, error) { return runtime.GetVPC(ctx, "test-vpc") }
	current := []Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}}
	desired := []Tag{{Key: "env", Value: "prod"}}

	updated, err := reconcileTags(context.Background(), runtime, "VPC", "test-vpc", "/vpcs/test-vpc", current, desired, read)
	if err != nil {
		t.Fatalf("reconcileTags() error = %v", err)
	}
	if !sameTags(updated.Tags, desired) {
		t.Fatalf("reconcileTags() tags = %#v, want %#v", updated.Tags, desired)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []TagPatch{{Key: "env", Value: "prod"}, {Key: "owner"}}
	if !reflect.DeepEqual(body.Tags, want) {
		t.Fatalf("patch body = %#v, want %#v", body.Tags, want)
	}
	if reads < 2 {
		t.Fatalf("read calls = %d, want at least 2: the reconciler must poll the authoritative read", reads)
	}
}

func TestReconcileTagsSkipsThePatchWhenNothingChanged(t *testing.T) {
	t.Parallel()
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		if r.Method == http.MethodPatch {
			patches++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": VPC{Name: "test-vpc", Status: "active", Tags: []Tag{{Key: "env", Value: "test"}}}})
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	runtime := NewRuntime(config)
	runtime.pollInterval = time.Millisecond
	runtime.lifecycleTimeout = time.Second

	read := func(ctx context.Context) (VPC, error) { return runtime.GetVPC(ctx, "test-vpc") }
	tags := []Tag{{Key: "env", Value: "test"}}
	if _, err := reconcileTags(context.Background(), runtime, "VPC", "test-vpc", "/vpcs/test-vpc", tags, tags, read); err != nil {
		t.Fatalf("reconcileTags() error = %v", err)
	}
	if patches != 0 {
		t.Fatalf("PATCH count = %d, want 0", patches)
	}
}
