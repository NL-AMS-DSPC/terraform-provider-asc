package tests

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

func TestRouteAllowlistShapes(t *testing.T) {
	cases := []struct {
		path    string
		allowed bool
	}{
		{"/api/infra-gateway/vpcs/", true},
		{"/api/infra-gateway/vms/test-vm", true},
		{"/api/uam/tenants/x/roles/", true},
		// The bare prefix is the service's own collection root, so it is
		// allowed exactly as written alongside the subtree under it.
		{"/api/uam", true},
		{"/api/infra-gateway", true},
		// Retired provider surfaces: these are the routes this boundary exists
		// to keep out, plus the near misses that a naive prefix match lets in.
		{"/api/vm/v1/namespaces/n/skus", false},
		{"/api/block-storage/v1/volumes", false},
		{"/api/network/v1/vpcs", false},
		{"/api/uamservice/tenants", false},
		{"/api/infra-gateway-legacy/vpcs/", false},
		{"/api", false},
		{"/", false},
		{"", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			if got := routeAllowed(testCase.path); got != testCase.allowed {
				t.Errorf("routeAllowed(%q) = %v, want %v", testCase.path, got, testCase.allowed)
			}
		})
	}
}

// captureT stands in for *testing.T so a failure the assertion reports can be
// inspected instead of failing the test that provoked it.
type captureT struct{ failures []string }

func (c *captureT) Helper() {}

func (c *captureT) Errorf(format string, args ...any) {
	c.failures = append(c.failures, fmt.Sprintf(format, args...))
}

func TestBoundaryAssertionIsLive(t *testing.T) {
	environment := startTestEnvironment(nil)
	t.Cleanup(environment.auth.Close)
	t.Cleanup(environment.api.Close)

	request, err := http.NewRequest(http.MethodGet, environment.api.URL+"/api/block-storage/v1/volumes", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	_ = response.Body.Close()

	routes := environment.recordedRoutes()
	if len(routes) == 0 {
		t.Fatal("recordedRoutes() is empty, the harness did not observe the request")
	}

	capture := &captureT{}
	assertRoutesAllowed(capture, routes, environment.recordedAuthRoutes())
	if len(capture.failures) != 1 {
		t.Fatalf("failures = %v, want exactly one violation", capture.failures)
	}
	if !strings.Contains(capture.failures[0], "/api/block-storage/v1/volumes") {
		t.Errorf("failure %q does not name the offending route", capture.failures[0])
	}
}

func TestBoundaryAuthAssertionIsLive(t *testing.T) {
	environment := startTestEnvironment(nil)
	t.Cleanup(environment.auth.Close)
	t.Cleanup(environment.api.Close)

	response, err := http.Get(environment.auth.URL + "/realms/test-realm/protocol/openid-connect/userinfo")
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	_ = response.Body.Close()

	capture := &captureT{}
	assertRoutesAllowed(capture, environment.recordedRoutes(), environment.recordedAuthRoutes())
	if len(capture.failures) != 1 {
		t.Fatalf("failures = %v, want exactly one violation", capture.failures)
	}
	if !strings.Contains(capture.failures[0], "openid-connect/userinfo") {
		t.Errorf("failure %q does not name the offending route", capture.failures[0])
	}
}

func TestBoundaryProviderStaysOnAllowedRoutes(t *testing.T) {
	var deleted atomic.Bool
	vpc := client.VPC{
		ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
		CIDR: "10.0.0.0/24", Status: "active",
		Subnets: []client.Subnet{{
			ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
			VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.0/25", Type: "private", Status: "active",
		}},
	}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vpcs/": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"created": vpc.Name})
		},
		"GET /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, vpc)
		},
		"DELETE /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			writeData(w, http.StatusOK, map[string]string{"deleted": vpc.Name})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
resource "asc_vpc" "test" {
  name = "test-vpc"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25", type = "private" }]
}`,
		Check: resource.TestCheckResourceAttr("asc_vpc.test", "id", vpc.ID),
	}}})

	apiRoutes := environment.recordedRoutes()
	authRoutes := environment.recordedAuthRoutes()
	if len(apiRoutes) == 0 || len(authRoutes) == 0 {
		t.Fatalf("recorded api routes %v and auth routes %v, want both non-empty", apiRoutes, authRoutes)
	}
	assertRoutesAllowed(t, apiRoutes, authRoutes)
}
