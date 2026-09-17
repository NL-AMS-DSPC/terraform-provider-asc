package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// volumeGateway is a stand-in for the infra-gateway volume endpoints. It keeps
// the read model the handlers serve so a test can move it out of band, the way
// an operator or another controller would.
type volumeGateway struct {
	mu        sync.Mutex
	current   client.Volume
	absent    bool
	creating  bool
	respell   func(string) string
	terminal  bool
	lastError string
}

func newVolumeGateway() *volumeGateway {
	return &volumeGateway{absent: true, respell: func(size string) string { return size }}
}

func (g *volumeGateway) handlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"POST /api/infra-gateway/volumes/": func(w http.ResponseWriter, r *http.Request) {
			var request client.CreateVolumeRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			g.current = client.Volume{
				URN:  "asc:atos:storage:test-tenant:block_volume:00000000-0000-4000-8000-000000000001",
				Name: request.Name, Size: request.Size, StorageClass: request.StorageClass,
				AccessMode: request.AccessMode, VolumeMode: "Filesystem", Status: "detached", Tags: request.Tags,
				Labels: map[string]string{"asc.io/volume": request.Name},
			}
			g.absent = false
			g.creating = true
			writeData(w, http.StatusCreated, map[string]string{"id": "00000000-0000-4000-8000-000000000001", "urn": g.current.URN, "name": request.Name})
		},
		"GET /api/infra-gateway/volumes/test-volume": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.absent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			view := g.current
			view.Size = g.respell(view.Size)
			switch {
			case g.terminal:
				view.Status = "error"
				view.LastError = g.lastError
			case g.creating:
				// The gateway acknowledges the command before the volume exists,
				// so the first read still reports it as provisioning.
				g.creating = false
				view.Status = "creating"
			}
			writeData(w, http.StatusOK, view)
		},
		"POST /api/infra-gateway/volumes/test-volume/resize": func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Size string `json:"size"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			g.current.Size = request.Size
			writeData(w, http.StatusOK, map[string]string{"name": g.current.Name, "size": request.Size})
		},
		"PATCH /api/infra-gateway/volumes/test-volume": func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Tags []client.Tag `json:"tags"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			g.current.Tags = applyTagPatch(g.current.Tags, request.Tags)
			// PatchVolume echoes the request's own entries back rather than the
			// resulting tag set, which is why the provider re-reads afterwards.
			echo := g.current
			echo.Tags = request.Tags
			writeData(w, http.StatusOK, echo)
		},
		"DELETE /api/infra-gateway/volumes/test-volume": func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.absent = true
			writeData(w, http.StatusOK, map[string]string{"deletedId": "test-volume"})
		},
	}
}

func (g *volumeGateway) mutate(change func(*volumeGateway)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	change(g)
}

// applyTagPatch mirrors infra-gateway's patch semantics: an entry sets its key,
// an entry with an empty value deletes it, and unmentioned keys are untouched.
func applyTagPatch(current, patch []client.Tag) []client.Tag {
	values := make(map[string]string, len(current))
	order := make([]string, 0, len(current)+len(patch))
	for _, tag := range current {
		if _, seen := values[tag.Key]; !seen {
			order = append(order, tag.Key)
		}
		values[tag.Key] = tag.Value
	}
	for _, tag := range patch {
		if tag.Value == "" {
			delete(values, tag.Key)
			continue
		}
		if _, seen := values[tag.Key]; !seen {
			order = append(order, tag.Key)
		}
		values[tag.Key] = tag.Value
	}
	result := make([]client.Tag, 0, len(values))
	for _, key := range order {
		if value, seen := values[key]; seen {
			result = append(result, client.Tag{Key: key, Value: value})
		}
	}
	return result
}

func volumeConfig(environment testEnvironment, size, tags string) string {
	return environment.providerConfig() + fmt.Sprintf(`
resource "asc_volume" "test" {
  name          = "test-volume"
  size          = %q
  storage_class = "standard"
  access_mode   = "ReadWriteOnce"
  tags          = %s
}`, size, tags)
}

func TestVolumeProvisioning(t *testing.T) {
	gateway := newVolumeGateway()
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: volumeConfig(environment, "10Gi", `{ env = "test" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_volume.test", "size", "10Gi"),
				resource.TestCheckResourceAttr("asc_volume.test", "size_bytes", "10737418240"),
				resource.TestCheckResourceAttr("asc_volume.test", "access_mode", "ReadWriteOnce"),
				resource.TestCheckResourceAttr("asc_volume.test", "volume_mode", "Filesystem"),
				resource.TestCheckResourceAttr("asc_volume.test", "status", "detached"),
				resource.TestCheckResourceAttr("asc_volume.test", "tags.env", "test"),
				resource.TestCheckResourceAttr("asc_volume.test", "labels.asc.io/volume", "test-volume"),
				resource.TestCheckResourceAttrSet("asc_volume.test", "urn"),
			),
		},
		// A plan straight after the apply must be empty.
		{Config: volumeConfig(environment, "10Gi", `{ env = "test" }`), PlanOnly: true},
		// A resize and a tag change in the same apply, both in place.
		{
			Config:           volumeConfig(environment, "20Gi", `{ env = "test", owner = "platform" }`),
			ConfigPlanChecks: inPlace("asc_volume.test"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_volume.test", "size", "20Gi"),
				resource.TestCheckResourceAttr("asc_volume.test", "size_bytes", "21474836480"),
				resource.TestCheckResourceAttr("asc_volume.test", "tags.owner", "platform"),
			),
		},
		// Dropping a tag needs the explicit empty-valued patch entry.
		{
			Config:           volumeConfig(environment, "20Gi", `{ env = "test" }`),
			ConfigPlanChecks: inPlace("asc_volume.test"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_volume.test", "tags.%", "1"),
				resource.TestCheckNoResourceAttr("asc_volume.test", "tags.owner"),
				resource.TestCheckResourceAttr("asc_volume.test", "size", "20Gi"),
			),
		},
		{ResourceName: "asc_volume.test", ImportState: true, ImportStateId: "test-volume", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name"},
		{Config: volumeConfig(environment, "20Gi", `{ env = "test" }`), PlanOnly: true},
	}})
}

func TestVolumeProvisioningKeepsTheConfiguredSizeSpelling(t *testing.T) {
	gateway := newVolumeGateway()
	// infra-gateway stores an opaque quantity and may echo an equal size spelled
	// differently, which must not show up as a diff.
	gateway.respell = func(size string) string {
		if size == "10Gi" {
			return "10240Mi"
		}
		return size
	}
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: volumeConfig(environment, "10Gi", `{ env = "test" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_volume.test", "size", "10Gi"),
				resource.TestCheckResourceAttr("asc_volume.test", "size_bytes", "10737418240"),
			),
		},
		{Config: volumeConfig(environment, "10Gi", `{ env = "test" }`), PlanOnly: true},
	}})
}

func TestVolumeProvisioningRefreshesExternalDrift(t *testing.T) {
	gateway := newVolumeGateway()
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: volumeConfig(environment, "10Gi", `{ env = "test" }`)},
		{
			// Something outside Terraform shrank the volume and retagged it, so
			// the refreshed state must differ from the configuration.
			PreConfig: func() {
				gateway.mutate(func(g *volumeGateway) {
					g.current.Size = "5Gi"
					g.current.Tags = []client.Tag{{Key: "env", Value: "drifted"}}
				})
			},
			Config: volumeConfig(environment, "10Gi", `{ env = "test" }`), PlanOnly: true, ExpectNonEmptyPlan: true,
		},
	}})
}

func TestVolumeProvisioningDetectsOutOfBandDeletion(t *testing.T) {
	gateway := newVolumeGateway()
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: volumeConfig(environment, "10Gi", `{ env = "test" }`)},
		{
			// The volume is gone, so the refresh must drop it from state and the
			// plan must want to create it again.
			PreConfig: func() { gateway.mutate(func(g *volumeGateway) { g.absent = true }) },
			Config:    volumeConfig(environment, "10Gi", `{ env = "test" }`), PlanOnly: true, ExpectNonEmptyPlan: true,
		},
	}})
}

func TestVolumeProvisioningSurfacesTerminalError(t *testing.T) {
	gateway := newVolumeGateway()
	gateway.terminal = true
	gateway.lastError = "no capacity in the requested storage class"
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config:      volumeConfig(environment, "10Gi", `{ env = "test" }`),
		ExpectError: regexp.MustCompile("entered error state"),
	}}})
}

func TestVolumeProvisioningRejectsShrinkAtPlanTime(t *testing.T) {
	gateway := newVolumeGateway()
	environment := newTestEnvironment(t, gateway.handlers())

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: volumeConfig(environment, "10Gi", `{ env = "test" }`)},
		{
			Config:      volumeConfig(environment, "5Gi", `{ env = "test" }`),
			ExpectError: regexp.MustCompile("Volume cannot be shrunk"),
		},
	}})
}
