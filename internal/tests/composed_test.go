package tests

import (
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// composedGateway is a small stateful stand-in for infra-gateway covering the
// four resources a realistic configuration chains together. It exists to prove
// the ordering Terraform derives from the references actually works against the
// gateway's own rules, which single-resource tests cannot show: a volume cannot
// be deleted while it is attached, and an attachment cannot be created before
// both its volume and its machine exist.
type composedGateway struct {
	mu sync.Mutex

	vpcPresent    bool
	vmPresent     bool
	volumePresent bool
	attachedToVM  string

	// order records the mutating calls in the order they arrived, which is what
	// the dependency ordering assertions read.
	order []string
}

func (g *composedGateway) record(call string) {
	g.order = append(g.order, call)
}

func (g *composedGateway) calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.order)
}

func (g *composedGateway) handlers() map[string]http.HandlerFunc {
	const vpcID = "00000000-0000-4000-8000-000000000001"

	return map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vpcs/": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.vpcPresent = true
			g.record("create vpc")
			writeData(w, http.StatusCreated, map[string]string{"created": "composed-vpc"})
		},
		"GET /api/infra-gateway/vpcs/composed-vpc": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.vpcPresent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, client.VPC{
				ID: vpcID, URN: "asc:vpc:composed-vpc", Name: "composed-vpc", CIDR: "10.0.0.0/24", Status: "active",
				Subnets: []client.Subnet{{
					ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
					VPCID: vpcID, CIDR: "10.0.0.0/25", Type: "private", Status: "active",
				}},
			})
		},
		"DELETE /api/infra-gateway/vpcs/composed-vpc": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.vpcPresent = false
			g.record("delete vpc")
			writeData(w, http.StatusOK, map[string]string{"deleted": "composed-vpc"})
		},

		"POST /api/infra-gateway/vms/": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.vmPresent = true
			g.record("create vm")
			writeData(w, http.StatusCreated, map[string]string{"created": "composed-vm"})
		},
		"GET /api/infra-gateway/vms/composed-vm": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.vmPresent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			vm := client.VM{
				URN: "asc:vm:composed-vm", Name: "composed-vm", VPCID: vpcID,
				SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
			}
			if g.attachedToVM == "composed-vm" {
				vm.AttachedVolumes = []string{"composed-volume"}
			}
			writeData(w, http.StatusOK, vm)
		},
		"DELETE /api/infra-gateway/vms/composed-vm": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.vmPresent = false
			g.record("delete vm")
			writeData(w, http.StatusOK, map[string]string{"deleted": "composed-vm"})
		},

		"POST /api/infra-gateway/volumes/": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.volumePresent = true
			g.record("create volume")
			writeData(w, http.StatusCreated, map[string]string{"id": "00000000-0000-4000-8000-000000000003", "name": "composed-volume"})
		},
		"GET /api/infra-gateway/volumes/composed-volume": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.volumePresent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			status := "detached"
			if g.attachedToVM != "" {
				status = "attached"
			}
			writeData(w, http.StatusOK, client.Volume{
				URN: "asc:volume:composed-volume", Name: "composed-volume", Size: "10Gi",
				StorageClass: "standard", AccessMode: "ReadWriteOnce", VolumeMode: "Filesystem",
				Status: status, AttachedToVM: g.attachedToVM,
			})
		},
		// The gateway refuses to delete a volume that is still attached. Keeping
		// that rule here is the point of the test: it only passes if Terraform
		// destroyed the attachment first.
		"DELETE /api/infra-gateway/volumes/composed-volume": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.attachedToVM != "" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			g.volumePresent = false
			g.record("delete volume")
			writeData(w, http.StatusOK, map[string]string{"deletedId": "composed-volume"})
		},

		// Attaching requires both ends to exist, exactly as the gateway's block
		// service checks before it dispatches the command.
		"POST /api/infra-gateway/volumes/composed-volume/attach/composed-vm": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.volumePresent || !g.vmPresent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			g.attachedToVM = "composed-vm"
			g.record("attach")
			writeData(w, http.StatusOK, map[string]string{"volumeName": "composed-volume", "vmName": "composed-vm"})
		},
		"DELETE /api/infra-gateway/volumes/composed-volume/attach/composed-vm": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.attachedToVM = ""
			g.record("detach")
			writeData(w, http.StatusOK, map[string]string{"volumeName": "composed-volume", "vmName": "composed-vm"})
		},
	}
}

func composedConfig(environment testEnvironment) string {
	return environment.providerConfig() + `
resource "asc_vpc" "test" {
  name    = "composed-vpc"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25", type = "private" }]
}

resource "asc_virtual_machine" "test" {
  name   = "composed-vm"
  sku_id = "gp-2"
  vpc_id = asc_vpc.test.id
}

resource "asc_volume" "test" {
  name          = "composed-volume"
  size          = "10Gi"
  storage_class = "standard"
  access_mode   = "ReadWriteOnce"
}

resource "asc_volume_attachment" "test" {
  volume_name = asc_volume.test.name
  vm_name     = asc_virtual_machine.test.name
}
`
}

// TestComposedInfrastructureAppliesAndDestroysInOrder applies the whole chain
// and then lets the framework destroy it, asserting the call order at both
// ends. The gateway rules encoded above mean a wrong order fails the run
// outright; the order assertions then say which rule was broken.
func TestComposedInfrastructureAppliesAndDestroysInOrder(t *testing.T) {
	gateway := &composedGateway{}
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy: func(*terraform.State) error {
			assertOrder(t, gateway.calls(), "detach", "delete volume")
			assertOrder(t, gateway.calls(), "delete vm", "delete vpc")
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: composedConfig(environment),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("asc_virtual_machine.test", "vpc_id", "asc_vpc.test", "id"),
					resource.TestCheckResourceAttr("asc_volume_attachment.test", "status", "attached"),
					// asc_volume and asc_virtual_machine were both created
					// before the attachment existed, so neither records it yet.
					// The next step shows the refresh picking it up.
					resource.TestCheckResourceAttr("asc_volume.test", "attached_to_vm", ""),
					resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.#", "0"),
					func(*terraform.State) error {
						calls := gateway.calls()
						assertOrder(t, calls, "create vpc", "create vm")
						assertOrder(t, calls, "create vm", "attach")
						assertOrder(t, calls, "create volume", "attach")
						return nil
					},
				),
			},
			// After a refresh both ends report the attachment, and the chain is
			// still stable: the framework fails this step if the plan that
			// follows it is not empty, which is what proves the two views of the
			// same attachment do not fight each other.
			{
				Config: composedConfig(environment),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("asc_volume.test", "attached_to_vm", "composed-vm"),
					resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.#", "1"),
					resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.0", "composed-volume"),
				),
			},
			{Config: composedConfig(environment), PlanOnly: true},
		},
	})
}

func assertOrder(t *testing.T, calls []string, first, second string) {
	t.Helper()
	firstAt, secondAt := slices.Index(calls, first), slices.Index(calls, second)
	if firstAt < 0 || secondAt < 0 {
		t.Errorf("calls = %v, want both %q and %q", calls, first, second)
		return
	}
	if firstAt > secondAt {
		t.Errorf("calls = %v, want %q before %q", calls, first, second)
	}
}
