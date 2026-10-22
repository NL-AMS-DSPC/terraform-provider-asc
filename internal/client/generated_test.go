package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/uam"
)

func newGeneratedTestRuntime(t *testing.T, base string, handler http.HandlerFunc) *Runtime {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL+base, "tenant", server.URL, "realm", "id", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	return NewRuntime(config)
}

func TestServiceURLKeepsEscapedEndpointPath(t *testing.T) {
	t.Parallel()

	endpoint, err := url.Parse("https://api.example.com/gateway%20prefix/")
	if err != nil {
		t.Fatal(err)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	endpoint.RawPath = ""

	if got, want := serviceURL(endpoint, infraGatewayPrefix), "https://api.example.com/gateway%20prefix/api/infra-gateway/"; got != want {
		t.Errorf("serviceURL = %q, want %q", got, want)
	}
}

// Generated requests carry the bearer token, the X-Tenant header and the escaped path, and
// a successful infra-gateway body is unwrapped from its data envelope.
func TestCallInfraSendsAuthorizedRequest(t *testing.T) {
	t.Parallel()

	var request, authorization, tenant string
	runtime := newGeneratedTestRuntime(t, "/gateway%20prefix", func(w http.ResponseWriter, r *http.Request) {
		request = r.Method + " " + r.URL.EscapedPath()
		authorization, tenant = r.Header.Get("Authorization"), r.Header.Get("X-Tenant")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"name": "a/b", "status": "ready"}})
	})

	var vpc infra.VPCResponse
	err := callInfra(context.Background(), func(ctx context.Context) (*http.Response, error) {
		return runtime.infraAPI().GetVPC(ctx, "a/b", &infra.GetVPCParams{XTenant: runtime.tenant()}, runtime.infraEditor())
	}, &vpc)
	if err != nil {
		t.Fatalf("callInfra: %v", err)
	}

	if want := "GET /gateway%20prefix/api/infra-gateway/vpcs/a%2Fb"; request != want {
		t.Errorf("request = %q, want %q", request, want)
	}
	if authorization != "Bearer token" || tenant != "tenant" {
		t.Errorf("Authorization = %q, X-Tenant = %q", authorization, tenant)
	}
	if value(vpc.Name) != "a/b" || value(vpc.Status) != "ready" {
		t.Errorf("VPC = %+v", vpc)
	}
}

func TestCallInfraRejectsMissingData(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{}`, `{"data":null}`} {
		runtime := newGeneratedTestRuntime(t, "", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		})
		var vpc infra.VPCResponse
		err := callInfra(context.Background(), func(ctx context.Context) (*http.Response, error) {
			return runtime.infraAPI().GetVPC(ctx, "vpc", &infra.GetVPCParams{XTenant: runtime.tenant()}, runtime.infraEditor())
		}, &vpc)
		if err == nil || !strings.Contains(err.Error(), "missing data") {
			t.Errorf("body %s: error = %v, want missing data", body, err)
		}
	}
}

// A UAM body is bare JSON, and UAM requests carry the same token and tenant.
func TestCallUAMDecodesBareJSON(t *testing.T) {
	t.Parallel()

	var request, authorization, tenant string
	runtime := newGeneratedTestRuntime(t, "", func(w http.ResponseWriter, r *http.Request) {
		request = r.Method + " " + r.URL.EscapedPath()
		authorization, tenant = r.Header.Get("Authorization"), r.Header.Get("X-Tenant")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "uuid-1"})
	})

	var result uam.TenantResponse
	err := callUAM(context.Background(), func(ctx context.Context) (*http.Response, error) {
		return runtime.uamAPI().GetTenant(ctx, "my tenant", runtime.uamEditor())
	}, &result)
	if err != nil {
		t.Fatalf("callUAM: %v", err)
	}
	if want := "GET /api/uam/v1/tenants/my%20tenant"; request != want {
		t.Errorf("request = %q, want %q", request, want)
	}
	if authorization != "Bearer token" || tenant != "tenant" {
		t.Errorf("Authorization = %q, X-Tenant = %q", authorization, tenant)
	}
	if value(result.ID) != "uuid-1" {
		t.Errorf("tenant = %+v", result)
	}
}

func TestGeneratedCallStatusClassification(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusNotFound, func(err error) bool { return errors.Is(err, ErrResourceNotFound) }},
		{http.StatusConflict, func(err error) bool { return errors.Is(err, ErrResourceConflict) }},
		{http.StatusBadGateway, func(err error) bool {
			return err != nil && err.Error() == "uam returned HTTP 502" && !errors.Is(err, ErrResourceNotFound)
		}},
	} {
		runtime := newGeneratedTestRuntime(t, "", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		})
		err := callUAM[uam.GroupListResponse](context.Background(), func(ctx context.Context) (*http.Response, error) {
			return runtime.uamAPI().ListTenantGroups(ctx, runtime.uamEditor())
		}, nil)
		if !tc.check(err) {
			t.Errorf("HTTP %d: error = %v", tc.status, err)
		}
	}
}

func TestGeneratedCallErrorIncludesResponseBody(t *testing.T) {
	t.Parallel()

	runtime := newGeneratedTestRuntime(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad_request","message":"bootDisk.image is required"}` + "\n"))
	})
	err := callUAM[uam.GroupListResponse](context.Background(), func(ctx context.Context) (*http.Response, error) {
		return runtime.uamAPI().ListTenantGroups(ctx, runtime.uamEditor())
	}, nil)
	want := `uam returned HTTP 400: {"error":"bad_request","message":"bootDisk.image is required"}`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}

func TestGeneratedCallAuthenticationFailure(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		requests++
	}))
	t.Cleanup(server.Close)
	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "id", "secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(config)

	err = callInfra[infra.VPCResponse](context.Background(), func(ctx context.Context) (*http.Response, error) {
		return runtime.infraAPI().DeleteVPC(ctx, "vpc", &infra.DeleteVPCParams{XTenant: runtime.tenant()}, runtime.infraEditor())
	}, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "authenticate infra-gateway request: ") {
		t.Errorf("error = %v, want an authentication error", err)
	}
	if requests != 0 {
		t.Errorf("API requests = %d, want 0", requests)
	}
}

func TestGeneratedCallHonoursCancellation(t *testing.T) {
	t.Parallel()

	runtime := newGeneratedTestRuntime(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := callInfra[infra.VPCResponse](ctx, func(ctx context.Context) (*http.Response, error) {
		return runtime.infraAPI().DeleteVPC(ctx, "vpc", &infra.DeleteVPCParams{XTenant: runtime.tenant()}, runtime.infraEditor())
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// "." and ".." would be removed by URL resolution and address a different route, so they
// are rejected before any request is sent.
func TestGeneratedCallsRejectDotSegments(t *testing.T) {
	t.Parallel()

	requests := 0
	runtime := newGeneratedTestRuntime(t, "", func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	})

	for _, name := range []string{".", ".."} {
		if err := runtime.DeleteVPC(context.Background(), name); err == nil || errors.Is(err, ErrResourceNotFound) {
			t.Errorf("DeleteVPC(%q) error = %v, want a path segment error", name, err)
		}
		if _, err := runtime.GetVolume(context.Background(), name); err == nil || errors.Is(err, ErrResourceNotFound) {
			t.Errorf("GetVolume(%q) error = %v, want a path segment error", name, err)
		}
		if err := runtime.DetachVolume(context.Background(), "data", name); err == nil || errors.Is(err, ErrResourceNotFound) {
			t.Errorf("DetachVolume(data, %q) error = %v, want a path segment error", name, err)
		}
	}
	if requests != 0 {
		t.Errorf("API requests = %d, want 0", requests)
	}
}
