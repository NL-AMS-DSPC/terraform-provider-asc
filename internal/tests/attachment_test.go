package tests

import (
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// attachmentFixture serves the volume read model that infra-gateway exposes and
// the acknowledgement-only attach and detach routes. The attachment itself has
// no read model of its own, so the volume is where every assertion lands.
type attachmentFixture struct {
	mu           sync.Mutex
	status       string
	attachedToVM string
	lastError    string
	absent       bool
	// settleAfter delays the end state by a number of reads, standing in for
	// the asynchronous command the gateway only acknowledges.
	settleAfter int
	reads       int
	pending     func(*attachmentFixture)
}

func (f *attachmentFixture) volume() client.Volume {
	return client.Volume{
		URN: "asc:volume:data", Name: "data", Size: "10Gi", StorageClass: "standard",
		AccessMode: "ReadWriteOnce", VolumeMode: "Filesystem",
		Status: f.status, LastError: f.lastError, AttachedToVM: f.attachedToVM,
	}
}

// appInstanceID is the value infra-gateway really puts in a volume's
// attachedToVM: the machine's instance uuid, not its name. The fixture serves
// it so the suite exercises the gateway's actual contract.
const (
	appInstanceID = "20000000-0000-4000-8000-000000000001"
	appVMURN      = "asc:atos:compute:tenant:virtual_machine:" + appInstanceID
)

func (f *attachmentFixture) handlers(t *testing.T) map[string]http.HandlerFunc {
	t.Helper()
	return map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vms/app": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, client.VM{
				URN: appVMURN, Name: "app", VPCID: "00000000-0000-4000-8000-000000000001",
				SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
			})
		},
		"POST /api/infra-gateway/volumes/": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"created": "data"})
		},
		"GET /api/infra-gateway/volumes/data": func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.absent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			f.reads++
			if f.pending != nil && f.reads > f.settleAfter {
				f.pending(f)
				f.pending = nil
			}
			writeData(w, http.StatusOK, f.volume())
		},
		"DELETE /api/infra-gateway/volumes/data": func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			f.absent = true
			f.mu.Unlock()
			writeData(w, http.StatusOK, map[string]string{"deletedId": "data"})
		},
		"POST /api/infra-gateway/volumes/data/attach/app": func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			f.status, f.reads = "attaching", 0
			f.pending = func(f *attachmentFixture) { f.status, f.attachedToVM = "attached", appInstanceID }
			f.mu.Unlock()
			writeData(w, http.StatusOK, map[string]string{"volumeName": "data", "vmName": "app"})
		},
		"DELETE /api/infra-gateway/volumes/data/attach/app": func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			f.status, f.reads = "detaching", 0
			f.pending = func(f *attachmentFixture) { f.status, f.attachedToVM = "detached", "" }
			f.mu.Unlock()
			writeData(w, http.StatusOK, map[string]string{"volumeName": "data", "vmName": "app"})
		},
	}
}

const attachmentConfig = `
resource "asc_volume_attachment" "test" {
  volume_name = "data"
  vm_name     = "app"
}`

func TestVolumeAttachmentLifecycle(t *testing.T) {
	fixture := &attachmentFixture{status: "detached", settleAfter: 1}
	environment := newTestEnvironment(t, fixture.handlers(t))

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: environment.providerConfig() + attachmentConfig,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_volume_attachment.test", "volume_name", "data"),
				resource.TestCheckResourceAttr("asc_volume_attachment.test", "vm_name", "app"),
				resource.TestCheckResourceAttr("asc_volume_attachment.test", "status", "attached"),
			),
		},
		{
			ResourceName: "asc_volume_attachment.test", ImportState: true, ImportStateId: "data:app",
			ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "volume_name",
		},
	}})
}

// TestVolumeAttachmentDetectsReassignment covers the drift that matters for an
// attachment: the volume still exists but now belongs to another machine, so
// this attachment is gone and the next plan has to recreate it.
func TestVolumeAttachmentDetectsReassignment(t *testing.T) {
	fixture := &attachmentFixture{status: "detached", settleAfter: 1}
	environment := newTestEnvironment(t, fixture.handlers(t))

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: environment.providerConfig() + attachmentConfig},
		{
			PreConfig: func() {
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				fixture.attachedToVM = "30000000-0000-4000-8000-000000000002"
			},
			Config:             environment.providerConfig() + attachmentConfig,
			ExpectNonEmptyPlan: true,
			PlanOnly:           true,
		},
	}})
}

// TestVolumeAttachmentHandlesOutOfBandVolumeDeletion proves the attachment
// leaves state when the volume it describes is gone, rather than erroring.
func TestVolumeAttachmentHandlesOutOfBandVolumeDeletion(t *testing.T) {
	fixture := &attachmentFixture{status: "detached", settleAfter: 1}
	environment := newTestEnvironment(t, fixture.handlers(t))

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: environment.providerConfig() + attachmentConfig},
		{
			PreConfig: func() {
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				fixture.absent = true
			},
			Config:             environment.providerConfig() + attachmentConfig,
			ExpectNonEmptyPlan: true,
			PlanOnly:           true,
		},
	}})
}

func TestVolumeAttachmentSurfacesATerminalError(t *testing.T) {
	fixture := &attachmentFixture{status: "detached", settleAfter: 0}
	handlers := fixture.handlers(t)
	handlers["POST /api/infra-gateway/volumes/data/attach/app"] = func(w http.ResponseWriter, _ *http.Request) {
		fixture.mu.Lock()
		fixture.status, fixture.lastError = "error", "no free device slot"
		fixture.mu.Unlock()
		writeData(w, http.StatusOK, map[string]string{"volumeName": "data", "vmName": "app"})
	}
	environment := newTestEnvironment(t, handlers)

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config:      environment.providerConfig() + attachmentConfig,
		ExpectError: regexp.MustCompile("no free device slot"),
	}}})
}
