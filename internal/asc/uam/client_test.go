package uam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The generated client keeps the server prefix, escapes both path parameters and parses a
// bare JSON body.
func TestGeneratedClientRequest(t *testing.T) {
	t.Parallel()

	var rawPath, edited string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawPath, edited = r.Method+" "+r.URL.EscapedPath(), r.Header.Get("X-Edited")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "ops/admin", "composites": []string{"b", "a"}})
	}))
	t.Cleanup(server.Close)

	client, err := NewClientWithResponses(server.URL+"/api/uam/",
		WithHTTPClient(server.Client()),
		WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("X-Edited", "yes")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	resp, err := client.GetTenantRoleWithResponse(context.Background(), "tenant id", "ops/admin")
	if err != nil {
		t.Fatalf("GetTenantRole: %v", err)
	}
	if got, want := rawPath, "GET /api/uam/v1/tenants/tenant%20id/roles/ops%2Fadmin"; got != want {
		t.Errorf("request = %q, want %q", got, want)
	}
	if edited != "yes" {
		t.Error("request editor did not run")
	}
	if resp.JSON200 == nil || *resp.JSON200.Name != "ops/admin" || len(*resp.JSON200.Composites) != 2 {
		t.Errorf("role = %+v", resp.JSON200)
	}
}

// Group list responses are the one UAM collection wrapped in an object.
func TestGeneratedGroupList(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"groups":[{"id":"g1","name":"ops","members":[{"name":"alice","enabled":true,"type":"user"}]}]}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}
	resp, err := client.ListTenantGroupsWithResponse(context.Background())
	if err != nil {
		t.Fatalf("ListTenantGroups: %v", err)
	}
	groups := *resp.JSON200.Groups
	if len(groups) != 1 || *groups[0].Name != "ops" || *(*groups[0].Members)[0].Name != "alice" {
		t.Errorf("groups = %+v", groups)
	}
}
