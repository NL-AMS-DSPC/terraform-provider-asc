package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newSKUTestRuntime(t *testing.T, handler http.HandlerFunc) *Runtime {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "id", "secret", 30*time.Second)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	return NewRuntime(config)
}

func TestListSKUsContract(t *testing.T) {
	t.Parallel()

	var path, authorization, tenant string
	runtime := newSKUTestRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		path = r.Method + " " + r.URL.Path
		authorization = r.Header.Get("Authorization")
		tenant = r.Header.Get("X-Tenant")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"id": "sku-1", "name": "gp-small", "family": "general-purpose",
			"threads": 2, "cores": 1, "memoryInMB": 2048, "storageInGB": 40,
			"storageType": "standard", "GPUCount": 1, "GPUType": "nvidia-a100",
		}}})
	})

	skus, err := runtime.ListSKUs(context.Background())
	if err != nil {
		t.Fatalf("ListSKUs: %v", err)
	}

	if path != "GET /api/infra-gateway/skus" {
		t.Errorf("request = %q, want GET /api/infra-gateway/skus", path)
	}
	if authorization != "Bearer token" {
		t.Errorf("Authorization = %q", authorization)
	}
	if tenant != "tenant" {
		t.Errorf("X-Tenant = %q", tenant)
	}
	if len(skus) != 1 {
		t.Fatalf("len(skus) = %d, want 1", len(skus))
	}
	got := skus[0]
	if got.ID != "sku-1" || got.Name != "gp-small" || got.Family != "general-purpose" {
		t.Errorf("identity = %+v", got)
	}
	if got.Threads != 2 || got.Cores != 1 || got.MemoryInMB != 2048 || got.StorageInGB != 40 {
		t.Errorf("capacity = %+v", got)
	}
	if got.StorageType != "standard" || got.GPUCount != 1 || got.GPUType != "nvidia-a100" {
		t.Errorf("storage/gpu = %+v", got)
	}
}

func TestListSKUsEmptyCatalogue(t *testing.T) {
	t.Parallel()

	runtime := newSKUTestRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	skus, err := runtime.ListSKUs(context.Background())
	if err != nil {
		t.Fatalf("ListSKUs: %v", err)
	}
	if len(skus) != 0 {
		t.Errorf("len(skus) = %d, want 0", len(skus))
	}
}

func TestListSKUsFailsWithoutState(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]int{
		"unauthorized": http.StatusUnauthorized,
		"forbidden":    http.StatusForbidden,
		"server":       http.StatusInternalServerError,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runtime := newSKUTestRuntime(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
					return
				}
				w.WriteHeader(status)
			})

			skus, err := runtime.ListSKUs(context.Background())
			if err == nil {
				t.Fatalf("ListSKUs succeeded on HTTP %d", status)
			}
			if skus != nil {
				t.Errorf("skus = %v, want nil on failure", skus)
			}
		})
	}
}

func TestListSKUsRejectsMalformedEnvelope(t *testing.T) {
	t.Parallel()

	runtime := newSKUTestRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":`))
	})

	if _, err := runtime.ListSKUs(context.Background()); err == nil {
		t.Fatal("ListSKUs accepted a malformed envelope")
	}
}

func TestListSKUsHonoursCancellation(t *testing.T) {
	t.Parallel()

	runtime := newSKUTestRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := runtime.ListSKUs(ctx); err == nil {
		t.Fatal("ListSKUs ignored a cancelled context")
	}
}
