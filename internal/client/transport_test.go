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

// Names are escaped exactly once below an endpoint whose own path is already escaped, on
// the token route, a generated infra-gateway route, a generated UAM route and the IPAM
// helper route.
func TestRequestEscapesPathSegmentsOnce(t *testing.T) {
	for _, name := range []string{"platform admins", "percent%name", "slash/name", "literal%2Fname"} {
		t.Run(name, func(t *testing.T) {
			const base = "/gateway%20prefix"
			wantAuth := base + "/realms/" + url.PathEscape(name) + "/protocol/openid-connect/token"
			wantVPC := base + "/api/infra-gateway/vpcs/" + url.PathEscape(name)
			wantUAM := base + "/api/uam/v1/tenants/" + url.PathEscape(name) + "/roles"
			wantIPAM := base + "/api/ipam/reservations"
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
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"name": name}})
				case wantUAM:
					_ = json.NewEncoder(w).Encode([]map[string]any{})
				case wantIPAM:
					_ = json.NewEncoder(w).Encode(map[string]any{"reservations": []any{}})
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
			// An empty role list makes the lookup report absence, which DeleteRole treats as done.
			if err := runtime.DeleteRole(context.Background(), "role"); err != nil {
				t.Fatalf("list roles: %v", err)
			}
			if _, err := runtime.ListReservations(context.Background()); err != nil {
				t.Fatalf("list reservations: %v", err)
			}
			for _, path := range []string{wantAuth, wantVPC, wantUAM, wantIPAM} {
				mu.Lock()
				if seen[path] != 1 {
					t.Errorf("requests to %q = %d, want 1", path, seen[path])
				}
				mu.Unlock()
			}
		})
	}
}

// The handwritten helper serves IPAM only; infra-gateway and UAM go through generated clients.
func TestLegacyTransportServesIPAMOnly(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, svc := range []svcType{"foo", svcInfraGateway, svcUAM} {
		var output any
		err = NewRuntime(config).get(context.Background(), svc, "/bar", &output)
		if err == nil || err.Error() != "unknown service type: "+string(svc) {
			t.Fatalf("error = %v, want unknown service type: %s", err, svc)
		}
	}
	if requests != 0 {
		t.Errorf("requests sent = %d, want 0", requests)
	}
}
