package tests

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// The volume attachment references the instance UUID carried by this VM URN.
const (
	composedVMID  = "20000000-0000-4000-8000-000000000004"
	composedVMURN = "asc:atos:compute:tenant:virtual_machine:" + composedVMID

	composedVNICID  = "20000000-0000-4000-8000-000000000005"
	composedVNICURN = "asc:atos:network:tenant:vnic:" + composedVNICID
)

// composedGateway enforces creation dependencies and rejects deletion of attached volumes
// and VNICs.
type composedGateway struct {
	mu sync.Mutex

	vpcPresent    bool
	vnicPresent   bool
	vnicVM        string
	vmPresent     bool
	volumePresent bool
	attachedToVM  string

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
		"POST /api/infra-gateway/vpcs": func(w http.ResponseWriter, _ *http.Request) {
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
					VPCID: vpcID, CIDR: "10.0.0.0/25", Status: "active",
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

		// A VNIC takes its IP from a subnet, so the VPC must exist first.
		"POST /api/infra-gateway/vnics": func(w http.ResponseWriter, r *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			var body struct {
				SubnetURN string `json:"subnetURN"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !g.vpcPresent || body.SubnetURN != "asc:subnet:bootstrap" {
				http.Error(w, `{"error":"subnet not found"}`, http.StatusBadRequest)
				return
			}
			g.vnicPresent = true
			g.record("create vnic")
			writeData(w, http.StatusCreated, g.vnic())
		},
		"GET /api/infra-gateway/vnics/" + composedVNICID: func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.vnicPresent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, g.vnic())
		},
		"DELETE /api/infra-gateway/vnics/" + composedVNICID: func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.vnicVM != "" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			g.vnicPresent = false
			g.record("delete vnic")
			writeData(w, http.StatusAccepted, map[string]string{"deleted": composedVNICID})
		},

		// A machine is created with existing VNICs and attaches them.
		"POST /api/infra-gateway/vms": func(w http.ResponseWriter, r *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			var body struct {
				VNICs []string `json:"vnics"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !g.vnicPresent || !slices.Equal(body.VNICs, []string{composedVNICURN}) {
				http.Error(w, `{"error":"vnic not found"}`, http.StatusBadRequest)
				return
			}
			g.vnicVM = composedVMURN
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
				URN: composedVMURN, Name: "composed-vm", VPCID: vpcID,
				SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
			}
			if g.attachedToVM == composedVMID {
				vm.AttachedVolumes = []string{"composed-volume"}
			}
			if g.vnicVM == composedVMURN {
				vm.VNICs = []string{composedVNICURN}
			}
			writeData(w, http.StatusOK, vm)
		},
		"DELETE /api/infra-gateway/vms/composed-vm": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.vmPresent = false
			g.vnicVM = ""
			g.record("delete vm")
			writeData(w, http.StatusOK, map[string]string{"deleted": "composed-vm"})
		},

		"POST /api/infra-gateway/volumes": func(w http.ResponseWriter, _ *http.Request) {
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
				VolumeMode: "Filesystem",
				Status:     status, AttachedToVM: g.attachedToVM,
			})
		},
		// Deletion must follow detachment.
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
			g.attachedToVM = composedVMID
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

func (g *composedGateway) vnic() client.VNIC {
	status := "detached"
	if g.vnicVM != "" {
		status = "attached"
	}
	return client.VNIC{
		URN: composedVNICURN, Name: "eth0", SubnetURN: "asc:subnet:bootstrap", VMURN: g.vnicVM,
		PrivateIP: "10.0.0.5", MAC: "00:00:00:00:00:05", Status: status,
	}
}

func composedConfig(environment testEnvironment) string {
	return environment.providerConfig() + `
resource "asc_vpc" "test" {
  name    = "composed-vpc"
  cidr    = "10.0.0.0/24"
  subnets = [{ name = "bootstrap", cidr = "10.0.0.0/25" }]
}

resource "asc_vnic" "test" {
  subnet_urn = asc_vpc.test.subnets[0].urn
}

resource "asc_virtual_machine" "test" {
  name   = "composed-vm"
  sku_id = "gp-2"
  vnics  = [asc_vnic.test.urn]
}

resource "asc_volume" "test" {
  name = "composed-volume"
  size = "10Gi"
}

resource "asc_volume_attachment" "test" {
  volume_name = asc_volume.test.name
  vm_name     = asc_virtual_machine.test.name
}
`
}

func TestComposedInfrastructureAppliesAndDestroysInOrder(t *testing.T) {
	gateway := &composedGateway{}
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories,
		CheckDestroy: func(*terraform.State) error {
			assertOrder(t, gateway.calls(), "detach", "delete volume")
			assertOrder(t, gateway.calls(), "delete vm", "delete vnic")
			assertOrder(t, gateway.calls(), "delete vnic", "delete vpc")
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: composedConfig(environment),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("asc_virtual_machine.test", "vpc_id", "asc_vpc.test", "id"),
					resource.TestCheckResourceAttrPair("asc_virtual_machine.test", "vnics.0", "asc_vnic.test", "urn"),
					resource.TestCheckResourceAttr("asc_vnic.test", "id", composedVNICID),
					resource.TestCheckResourceAttr("asc_vnic.test", "private_ip", "10.0.0.5"),
					resource.TestCheckResourceAttr("asc_volume_attachment.test", "status", "attached"),
					// Both resources were created before the attachment; the next refresh records it.
					resource.TestCheckResourceAttr("asc_volume.test", "attached_to_vm", ""),
					resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.#", "0"),
					func(*terraform.State) error {
						calls := gateway.calls()
						assertOrder(t, calls, "create vpc", "create vnic")
						assertOrder(t, calls, "create vnic", "create vm")
						assertOrder(t, calls, "create vm", "attach")
						assertOrder(t, calls, "create volume", "attach")
						return nil
					},
				),
			},
			// Refresh both ends and require the subsequent plan to stay empty.
			{
				Config: composedConfig(environment),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("asc_volume.test", "attached_to_vm", composedVMID),
					resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.#", "1"),
					resource.TestCheckResourceAttr("asc_virtual_machine.test", "attached_volumes.0", "composed-volume"),
					resource.TestCheckResourceAttr("asc_vnic.test", "status", "attached"),
					resource.TestCheckResourceAttr("asc_vnic.test", "vm_urn", composedVMURN),
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
