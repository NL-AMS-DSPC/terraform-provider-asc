package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// ipAllocationFixture delays the VM projection after acknowledging allocate and deallocate commands
type ipAllocationFixture struct {
	mu        sync.Mutex
	publicIPs []string
	status    string
	lastError string
	// settleAfter delays the end state by a number of reads, standing in for
	// the asynchronous command the gateway only acknowledges.
	settleAfter int
	reads       int
	pending     func(*ipAllocationFixture)
}

func (f *ipAllocationFixture) handlers(t *testing.T) map[string]http.HandlerFunc {
	t.Helper()
	decode := func(r *http.Request) string {
		var body client.IPAllocationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s %s body: %v", r.Method, r.URL.Path, err)
		}
		return body.IP
	}
	return map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vms/app": func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.reads++
			if f.pending != nil && f.reads > f.settleAfter {
				f.pending(f)
				f.pending = nil
			}
			internalIP := "192.168.0.10"
			writeData(w, http.StatusOK, client.VM{
				URN: appVMURN, Name: "app", VPCID: "00000000-0000-4000-8000-000000000001",
				SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: f.status, LastError: f.lastError,
				Addresses: client.Addresses{InternalIP: &internalIP, PublicIPs: slices.Clone(f.publicIPs)},
			})
		},
		"POST /api/infra-gateway/vms/app/ip": func(w http.ResponseWriter, r *http.Request) {
			ip := decode(r)
			f.mu.Lock()
			f.reads = 0
			f.pending = func(f *ipAllocationFixture) { f.publicIPs = append(f.publicIPs, ip) }
			f.mu.Unlock()
			writeData(w, http.StatusAccepted, map[string]string{"vmName": "app", "ip": ip})
		},
		"DELETE /api/infra-gateway/vms/app/ip": func(w http.ResponseWriter, r *http.Request) {
			ip := decode(r)
			f.mu.Lock()
			f.reads = 0
			f.pending = func(f *ipAllocationFixture) {
				f.publicIPs = slices.DeleteFunc(f.publicIPs, func(listed string) bool { return listed == ip })
			}
			f.mu.Unlock()
			writeData(w, http.StatusAccepted, map[string]string{"vmName": "app", "ip": ip})
		},
	}
}

func ipAllocationConfig(ip string) string {
	return fmt.Sprintf(`
resource "asc_ip_allocation" "test" {
  vm_name = "app"
  ip      = %q
}`, ip)
}

func TestIPAllocationLifecycle(t *testing.T) {
	fixture := &ipAllocationFixture{status: "active", settleAfter: 1}
	environment := newTestEnvironment(t, fixture.handlers(t))
	config := environment.providerConfig() + ipAllocationConfig("10.0.0.1") + `
data "asc_virtual_machine" "app" {
  name       = "app"
  depends_on = [asc_ip_allocation.test]
}`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy: func(*terraform.State) error {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.publicIPs) != 0 {
				return fmt.Errorf("public IPs left after destroy = %v, want none", fixture.publicIPs)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("asc_ip_allocation.test", "vm_name", "app"),
					resource.TestCheckResourceAttr("asc_ip_allocation.test", "ip", "10.0.0.1"),
					resource.TestCheckResourceAttr("data.asc_virtual_machine.app", "internal_ip", "192.168.0.10"),
					resource.TestCheckResourceAttr("data.asc_virtual_machine.app", "public_ips.#", "1"),
					resource.TestCheckResourceAttr("data.asc_virtual_machine.app", "public_ips.0", "10.0.0.1"),
				),
			},
			{
				ResourceName: "asc_ip_allocation.test", ImportState: true, ImportStateId: "app:10.0.0.1",
				ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "vm_name",
			},
			{
				ResourceName: "asc_ip_allocation.test", ImportState: true, ImportStateId: "app:10.0.0.01",
				ExpectError: regexp.MustCompile("Invalid IP address"),
			},
		},
	})
}

func TestIPAllocationHandlesOutOfBandDeallocation(t *testing.T) {
	fixture := &ipAllocationFixture{status: "active", settleAfter: 1}
	environment := newTestEnvironment(t, fixture.handlers(t))
	config := environment.providerConfig() + ipAllocationConfig("10.0.0.1")

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config},
		{
			PreConfig: func() {
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				fixture.publicIPs = nil
			},
			Config:             config,
			ExpectNonEmptyPlan: true,
			PlanOnly:           true,
		},
	}})
}

func TestIPAllocationSurfacesATerminalError(t *testing.T) {
	fixture := &ipAllocationFixture{status: "active"}
	handlers := fixture.handlers(t)
	handlers["POST /api/infra-gateway/vms/app/ip"] = func(w http.ResponseWriter, _ *http.Request) {
		fixture.mu.Lock()
		fixture.status, fixture.lastError = "error", "address is not reserved"
		fixture.mu.Unlock()
		writeData(w, http.StatusAccepted, map[string]string{"vmName": "app", "ip": "10.0.0.1"})
	}
	environment := newTestEnvironment(t, handlers)

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config:      environment.providerConfig() + ipAllocationConfig("10.0.0.1"),
		ExpectError: regexp.MustCompile("address is not reserved"),
	}}})
}

func TestIPAllocationRejectsAnythingButACanonicalIPv4Address(t *testing.T) {
	for _, ip := range []string{"10.0.0.01", "2001:db8::1", "app"} {
		t.Run(ip, func(t *testing.T) {
			environment := newTestEnvironment(t, nil)
			resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
				Config:      environment.providerConfig() + ipAllocationConfig(ip),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile("must be an IPv4 address"),
			}}})
		})
	}
}
