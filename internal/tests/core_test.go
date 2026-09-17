package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
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

// inPlace asserts the step updates the resource instead of replacing it. The
// assertion is explicit because a replacement still reaches the same end state
// and would pass every attribute check while silently destroying the resource.
func inPlace(address string) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
		plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate),
	}}
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

	// The inline subnet drives the replacement. Tags are patched in place now,
	// so changing one no longer replaces the VPC; the subnets list is the
	// remaining configurable field that infra-gateway cannot change on a live
	// VPC, and it keeps the name the same, which is what the name reservation
	// under test needs.
	currentVPC := func() client.VPC {
		cidr := "10.0.0.0/25"
		if posts >= 3 {
			cidr = "10.0.0.0/26"
		}
		return client.VPC{
			ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
			CIDR: "10.0.0.0/24", Status: "active",
			Subnets: []client.Subnet{{
				ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
				VPCID: "00000000-0000-4000-8000-000000000001", CIDR: cidr, Type: "private", Status: "active",
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

	config := func(cidr string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_vpc" "test" {
  name = "test-vpc"
  subnets = [{ name = "bootstrap", cidr = %q, type = "private" }]
}`, cidr)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config("10.0.0.0/25"), Check: resource.TestCheckResourceAttr("asc_vpc.test", "subnets.0.cidr", "10.0.0.0/25")},
		{Config: config("10.0.0.0/26"), Check: resource.TestCheckResourceAttr("asc_vpc.test", "subnets.0.cidr", "10.0.0.0/26")},
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

// taggedHandlers serves one resource whose tags start at current and move to
// whatever a PATCH asks for, applying the gateway's own patch semantics: an
// entry with an empty value deletes its key. It answers the PATCH the way the
// virtual machine handler does, echoing only the patch entries, so a resource
// that trusted the PATCH body instead of re-reading would fail these tests.
func taggedTagStore(initial map[string]string) (func() []client.Tag, http.HandlerFunc) {
	var mu sync.Mutex
	tags := map[string]string{}
	for key, value := range initial {
		tags[key] = value
	}

	read := func() []client.Tag {
		mu.Lock()
		defer mu.Unlock()
		keys := make([]string, 0, len(tags))
		for key := range tags {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make([]client.Tag, 0, len(keys))
		for _, key := range keys {
			result = append(result, client.Tag{Key: key, Value: tags[key]})
		}
		return result
	}

	patch := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tags []client.Tag `json:"tags"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		for _, tag := range body.Tags {
			if tag.Value == "" {
				delete(tags, tag.Key)
				continue
			}
			tags[tag.Key] = tag.Value
		}
		mu.Unlock()
		writeData(w, http.StatusOK, map[string]any{"tags": body.Tags})
	}
	return read, patch
}

func TestVPCTagsUpdateInPlace(t *testing.T) {
	var deleted atomic.Bool
	tags, patch := taggedTagStore(map[string]string{"env": "test", "owner": "platform"})
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vpcs/": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"created": "test-vpc"})
		},
		"GET /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, client.VPC{
				ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
				CIDR: "10.0.0.0/24", Status: "active", Tags: tags(),
				Subnets: []client.Subnet{{
					ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
					VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.0/25", Type: "private", Status: "active",
				}},
			})
		},
		"PATCH /api/infra-gateway/vpcs/test-vpc": patch,
		"DELETE /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			writeData(w, http.StatusOK, map[string]string{"deleted": "test-vpc"})
		},
	})

	config := func(tags string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_vpc" "test" {
  name = "test-vpc"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25", type = "private" }]
  tags = %s
}`, tags)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: config(`{ env = "test", owner = "platform" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_vpc.test", "tags.env", "test"),
				resource.TestCheckResourceAttr("asc_vpc.test", "tags.owner", "platform"),
			),
		},
		{
			// A changed value and a removed key in one step. The removal only
			// lands if the provider sent an explicit empty-valued patch entry,
			// and the surviving id proves the VPC was not replaced.
			Config:           config(`{ env = "prod" }`),
			ConfigPlanChecks: inPlace("asc_vpc.test"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_vpc.test", "tags.env", "prod"),
				resource.TestCheckNoResourceAttr("asc_vpc.test", "tags.owner"),
				resource.TestCheckResourceAttr("asc_vpc.test", "id", "00000000-0000-4000-8000-000000000001"),
			),
		},
	}})
}

func TestSubnetTagsUpdateInPlace(t *testing.T) {
	var deleted atomic.Bool
	tags, patch := taggedTagStore(map[string]string{"env": "test"})
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vpcs/test-vpc/subnets/": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"created": "test-subnet"})
		},
		"GET /api/infra-gateway/vpcs/test-vpc/subnets/": func(w http.ResponseWriter, _ *http.Request) {
			if deleted.Load() {
				writeData(w, http.StatusOK, []client.Subnet{})
				return
			}
			writeData(w, http.StatusOK, []client.Subnet{{
				ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:test-subnet", Name: "test-subnet",
				VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.128/25", Type: "public",
				Status: "active", Tags: tags(),
			}})
		},
		"PATCH /api/infra-gateway/vpcs/test-vpc/subnets/test-subnet": patch,
		"DELETE /api/infra-gateway/vpcs/test-vpc/subnets/test-subnet": func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			writeData(w, http.StatusOK, map[string]string{"deleted": "test-subnet"})
		},
	})

	config := func(tags string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_subnet" "test" {
  name = "test-subnet"
  vpc_name = "test-vpc"
  vpc_id = "00000000-0000-4000-8000-000000000001"
  cidr = "10.0.0.128/25"
  type = "public"
  tags = %s
}`, tags)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config(`{ env = "test" }`), Check: resource.TestCheckResourceAttr("asc_subnet.test", "tags.env", "test")},
		{
			Config:           config(`{ env = "prod", tier = "public" }`),
			ConfigPlanChecks: inPlace("asc_subnet.test"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_subnet.test", "tags.env", "prod"),
				resource.TestCheckResourceAttr("asc_subnet.test", "tags.tier", "public"),
				resource.TestCheckResourceAttr("asc_subnet.test", "id", "00000000-0000-4000-8000-000000000002"),
			),
		},
	}})
}

func TestVirtualMachineTagsUpdateInPlaceAndVolumesStaySorted(t *testing.T) {
	tags, patch := taggedTagStore(map[string]string{"env": "test", "owner": "platform"})
	var reads atomic.Int64
	var deleted atomic.Bool
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vms/": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"created": "test-vm"})
		},
		"GET /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) {
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			// The gateway assembles the attachments from a map, so the order it
			// serves them in changes between reads. Alternating here proves the
			// provider sorts rather than storing whatever arrived.
			volumes := []string{"vol-c", "vol-a", "vol-b"}
			if reads.Add(1)%2 == 0 {
				volumes = []string{"vol-b", "vol-c", "vol-a"}
			}
			writeData(w, http.StatusOK, client.VM{
				URN: "asc:vm:test-vm", Name: "test-vm", VPCID: "00000000-0000-4000-8000-000000000001",
				SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
				Tags: tags(), AttachedVolumes: volumes,
			})
		},
		"PATCH /api/infra-gateway/vms/test-vm": patch,
		"DELETE /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			writeData(w, http.StatusOK, map[string]string{"deleted": "test-vm"})
		},
	})

	config := func(tags string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_virtual_machine" "test" {
  name = "test-vm"
  sku_id = "gp-2"
  vpc_id = "00000000-0000-4000-8000-000000000001"
  tags = %s
}`, tags)
	}
	sortedVolumes := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.0", "vol-a"),
		resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.1", "vol-b"),
		resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.2", "vol-c"),
	)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: config(`{ env = "test", owner = "platform" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_virtual_machine.test", "tags.env", "test"), sortedVolumes,
			),
		},
		{
			Config:           config(`{ env = "prod" }`),
			ConfigPlanChecks: inPlace("asc_virtual_machine.test"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_virtual_machine.test", "tags.env", "prod"),
				resource.TestCheckNoResourceAttr("asc_virtual_machine.test", "tags.owner"),
				resource.TestCheckResourceAttr("asc_virtual_machine.test", "urn", "asc:vm:test-vm"),
				sortedVolumes,
			),
		},
	}})
}
