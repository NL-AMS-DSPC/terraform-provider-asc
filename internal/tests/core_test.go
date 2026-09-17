package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
	ascprovider "github.com/nl-ams-dspc/terraform-provider-asc/internal/provider"
)

var providerFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"asc": providerserver.NewProtocol6WithError(ascprovider.New("test")()),
}

type testEnvironment struct {
	api  *httptest.Server
	auth *httptest.Server
}

func newTestEnvironment(t *testing.T, handlers map[string]http.HandlerFunc) testEnvironment {
	t.Helper()
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realms/test-realm/protocol/openid-connect/token" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "expires_in": 3600})
	}))
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("X-Tenant") != "test-tenant" {
			http.Error(w, "missing authentication or tenant", http.StatusUnauthorized)
			return
		}
		handler, ok := handlers[r.Method+" "+r.URL.Path]
		if !ok {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(auth.Close)
	t.Cleanup(api.Close)
	return testEnvironment{api: api, auth: auth}
}

func (e testEnvironment) providerConfig() string {
	return fmt.Sprintf(`
provider "asc" {
  endpoint  = %q
  namespace = "test-tenant"
  auth_url  = %q
  org       = "test-realm"
  username  = "test-client"
  password  = "test-secret"
}
`, e.api.URL, e.auth.URL)
}

func writeData(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
}

func TestVPCProvisioning(t *testing.T) {
	var deleted atomic.Bool
	vpc := client.VPC{
		ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
		CIDR: "10.0.0.0/24", Status: "active", Tags: []client.Tag{{Key: "env", Value: "test"}},
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

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: environment.providerConfig() + `
resource "asc_vpc" "test" {
  name = "test-vpc"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25", type = "private" }]
  tags = { env = "test" }
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_vpc.test", "id", vpc.ID),
				resource.TestCheckResourceAttr("asc_vpc.test", "subnets.0.vpc_id", vpc.ID),
				resource.TestCheckResourceAttr("asc_vpc.test", "tags.env", "test"),
			),
		},
		{ResourceName: "asc_vpc.test", ImportState: true, ImportStateId: vpc.Name, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name"},
	}})
}

func TestSubnetProvisioning(t *testing.T) {
	subnet := client.Subnet{
		ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:test-subnet", Name: "test-subnet",
		VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.128/25", Type: "public", Status: "active",
	}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vpcs/test-vpc/subnets/": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"created": subnet.Name})
		},
		"GET /api/infra-gateway/vpcs/test-vpc/subnets/": func(w http.ResponseWriter, _ *http.Request) { writeData(w, http.StatusOK, []client.Subnet{subnet}) },
		"DELETE /api/infra-gateway/vpcs/test-vpc/subnets/test-subnet": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, map[string]string{"deleted": subnet.Name})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: environment.providerConfig() + `
resource "asc_subnet" "test" {
  name = "test-subnet"
  vpc_name = "test-vpc"
  vpc_id = "00000000-0000-4000-8000-000000000001"
  cidr = "10.0.0.128/25"
  type = "public"
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_subnet.test", "id", subnet.ID),
				resource.TestCheckResourceAttr("asc_subnet.test", "vpc_id", subnet.VPCID),
			),
		},
		{ResourceName: "asc_subnet.test", ImportState: true, ImportStateId: "test-vpc:test-subnet", ImportStateVerify: true},
	}})
}

func TestVirtualMachineProvisioning(t *testing.T) {
	var deleted atomic.Bool
	vm := client.VM{
		URN: "asc:vm:test-vm", Name: "test-vm", VPCID: "00000000-0000-4000-8000-000000000001",
		SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
		OS:   client.OS{ID: "ubuntu-24.04", Family: "linux", Distribution: "ubuntu", Release: "24.04", DisplayName: "Ubuntu 24.04"},
		Tags: []client.Tag{{Key: "env", Value: "test"}}, EnableLogging: true,
	}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vms/": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"created": vm.Name})
		},
		"GET /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) {
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, vm)
		},
		"DELETE /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			writeData(w, http.StatusOK, map[string]string{"deleted": vm.Name})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: environment.providerConfig() + `
resource "asc_virtual_machine" "test" {
  name = "test-vm"
  sku_id = "gp-2"
  vpc_id = "00000000-0000-4000-8000-000000000001"
  image = "ubuntu-24.04"
  enable_logging = true
  tags = { env = "test" }
  customization = { cloud_init = { user_data = "#cloud-config" } }
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_virtual_machine.test", "urn", vm.URN),
				resource.TestCheckResourceAttr("asc_virtual_machine.test", "vpc_id", vm.VPCID),
				resource.TestCheckResourceAttr("asc_virtual_machine.test", "enable_logging", "true"),
				resource.TestCheckResourceAttr("asc_virtual_machine.test", "customization.cloud_init.user_data", "#cloud-config"),
			),
		},
		{ResourceName: "asc_virtual_machine.test", ImportState: true, ImportStateId: vm.Name, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name", ImportStateVerifyIgnore: []string{"customization"}},
	}})
}

func TestVirtualMachineProvisioningRejectsNonCanonicalName(t *testing.T) {
	environment := newTestEnvironment(t, nil)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
resource "asc_virtual_machine" "test" {
  name = "My-VM"
  sku_id = "gp-2"
  vpc_id = "00000000-0000-4000-8000-000000000001"
}`,
		ExpectError: regexp.MustCompile("must start with a lowercase letter"),
	}}})
}

func TestVPCProvisioningReplacementWaitsForNameRelease(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	deleting := false
	deletionReads := 0

	currentVPC := func() client.VPC {
		version := "one"
		if posts >= 3 {
			version = "two"
		}
		return client.VPC{
			ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
			CIDR: "10.0.0.0/24", Status: "active", Tags: []client.Tag{{Key: "version", Value: version}},
			Subnets: []client.Subnet{{
				ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
				VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.0/25", Type: "private", Status: "active",
			}},
		}
	}

	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vpcs/": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			posts++
			if posts == 2 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			writeData(w, http.StatusCreated, map[string]string{"created": "test-vpc"})
		},
		"GET /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if deleting {
				deletionReads++
				if deletionReads == 1 {
					vpc := currentVPC()
					vpc.Status = "deleting"
					writeData(w, http.StatusOK, vpc)
					return
				}
				deleting = false
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if posts == 2 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, currentVPC())
		},
		"DELETE /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			deleting = true
			deletionReads = 0
			mu.Unlock()
			writeData(w, http.StatusOK, map[string]string{"deleted": "test-vpc"})
		},
	})

	config := func(version string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_vpc" "test" {
  name = "test-vpc"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25", type = "private" }]
  tags = { version = %q }
}`, version)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config("one"), Check: resource.TestCheckResourceAttr("asc_vpc.test", "tags.version", "one")},
		{Config: config("two"), Check: resource.TestCheckResourceAttr("asc_vpc.test", "tags.version", "two")},
	}})

	mu.Lock()
	defer mu.Unlock()
	if posts != 3 {
		t.Fatalf("POST count = %d, want 3 (initial, stale conflict, retry)", posts)
	}
}

func TestVirtualMachineProvisioningReplacementWaitsForNameRelease(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	deleting := false
	deletionReads := 0

	currentVM := func() client.VM {
		skuID := "gp-2"
		if posts >= 3 {
			skuID = "gp-4"
		}
		return client.VM{
			URN: "asc:vm:test-vm", Name: "test-vm", VPCID: "00000000-0000-4000-8000-000000000001",
			SKU: client.SKU{ID: skuID, Name: "general-purpose"}, Status: "active",
		}
	}

	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vms/": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			posts++
			if posts == 2 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			writeData(w, http.StatusCreated, map[string]string{"created": "test-vm"})
		},
		"GET /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if deleting {
				deletionReads++
				if deletionReads == 1 {
					vm := currentVM()
					vm.Status = "deleting"
					writeData(w, http.StatusOK, vm)
					return
				}
				deleting = false
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if posts == 2 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, currentVM())
		},
		"DELETE /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			deleting = true
			deletionReads = 0
			mu.Unlock()
			writeData(w, http.StatusOK, map[string]string{"deleted": "test-vm"})
		},
	})

	config := func(skuID string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_virtual_machine" "test" {
  name = "test-vm"
  sku_id = %q
  vpc_id = "00000000-0000-4000-8000-000000000001"
}`, skuID)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config("gp-2"), Check: resource.TestCheckResourceAttr("asc_virtual_machine.test", "sku.id", "gp-2")},
		{Config: config("gp-4"), Check: resource.TestCheckResourceAttr("asc_virtual_machine.test", "sku.id", "gp-4")},
	}})

	mu.Lock()
	defer mu.Unlock()
	if posts != 3 {
		t.Fatalf("POST count = %d, want 3 (initial, stale conflict, retry)", posts)
	}
}
