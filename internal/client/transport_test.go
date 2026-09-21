package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

func TestRequestEscapesPathSegmentsOnce(t *testing.T) {
	for _, name := range []string{"platform admins", "percent%name", "slash/name", "literal%2Fname"} {
		t.Run(name, func(t *testing.T) {
			const base = "/gateway%20prefix"
			wantAuth := base + "/realms/" + url.PathEscape(name) + "/protocol/openid-connect/token"
			wantVPC := base + "/api/infra-gateway/vpcs/" + url.PathEscape(name)
			wantUAM := base + "/api/uam/v1/tenants/" + url.PathEscape(name) + "/roles"
			seen := make(map[string]int)
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.EscapedPath()
				mu.Lock()
				seen[path]++
				mu.Unlock()
				switch path {
				case wantAuth:
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 300})
				case wantVPC:
					_ = json.NewEncoder(w).Encode(map[string]any{"data": VPC{Name: name}})
				case wantUAM:
					_ = json.NewEncoder(w).Encode([]roleResponse{})
				default:
					t.Errorf("unexpected escaped path %q", path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			config, err := NewConfig(server.URL+base, name, server.URL+base, name, "client", "secret", 0)
			if err != nil {
				t.Fatal(err)
			}
			runtime := NewRuntime(config)
			if _, err := runtime.GetVPC(context.Background(), name); err != nil {
				t.Fatalf("get VPC: %v", err)
			}
			var roles []roleResponse
			if err := runtime.uamGet(context.Background(), runtime.tenantRolesPath(), &roles); err != nil {
				t.Fatalf("list roles: %v", err)
			}
			for _, path := range []string{wantAuth, wantVPC, wantUAM} {
				mu.Lock()
				if seen[path] != 1 {
					t.Errorf("requests to %q = %d, want 1", path, seen[path])
				}
				mu.Unlock()
			}
		})
	}
}
