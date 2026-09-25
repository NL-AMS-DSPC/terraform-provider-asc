package tests

import (
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// The gateway omits VPC ID, image and customization from group reads.
func testVMGroup() client.VMGroup {
	return client.VMGroup{
		URN: "asc:vm_group:test-group", Name: "test-group",
		SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
		Tags: []client.Tag{{Key: "env", Value: "test"}},
		// Reported in map order by the gateway; the provider sorts it.
		AttachedVolumes: []string{"logs", "data"},
		AutoscalingPolicy: client.AutoscalingPolicy{
			MinReplicas: 2, MaxReplicas: 6,
			Cron: &client.CronRule{Timezone: "Europe/Amsterdam", Start: "0 8 * * 1-5", End: "0 18 * * 1-5", DesiredReplicas: 4},
		},
	}
}

func vmGroupConfig(environment testEnvironment) string {
	return environment.providerConfig() + `
resource "asc_vm_group" "test" {
  name = "test-group"
  sku_id = "gp-2"
  vpc_id = "00000000-0000-4000-8000-000000000001"
  image = "ubuntu-24.04"
  autoscaling = {
    min_replicas = 2
    max_replicas = 6
    cron = { timezone = "Europe/Amsterdam", start = "0 8 * * 1-5", end = "0 18 * * 1-5", desired_replicas = 4 }
  }
  tags = { env = "test" }
  customization = { cloud_init = { user_data = "#cloud-config" } }
}`
}

func TestVMGroupProvisioning(t *testing.T) {
	var deleted atomic.Bool
	group := testVMGroup()
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"id": "00000000-0000-4000-8000-000000000009", "urn": group.URN, "name": group.Name})
		},
		"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, group)
		},
		"DELETE /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			writeData(w, http.StatusOK, map[string]string{"deleted": group.Name})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: vmGroupConfig(environment),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_vm_group.test", "urn", group.URN),
				resource.TestCheckResourceAttr("asc_vm_group.test", "sku.id", "gp-2"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "status", "active"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "tags.env", "test"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "autoscaling.min_replicas", "2"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "autoscaling.cron.desired_replicas", "4"),
				// Configuration-only: the gateway serves none of these back, so
				// they have to survive untouched in state.
				resource.TestCheckResourceAttr("asc_vm_group.test", "vpc_id", "00000000-0000-4000-8000-000000000001"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "image", "ubuntu-24.04"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "customization.cloud_init.user_data", "#cloud-config"),
				// Sorted by the client: the gateway builds this list from a map.
				resource.TestCheckResourceAttr("asc_vm_group.test", "attached_volumes.0", "data"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "attached_volumes.1", "logs"),
			),
		},
		{Config: vmGroupConfig(environment), PlanOnly: true},
		{
			ResourceName: "asc_vm_group.test", ImportState: true, ImportStateId: group.Name,
			ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name",
			// The gateway cannot recover these write-only fields during import.
			ImportStateVerifyIgnore: []string{"vpc_id", "image", "customization"},
		},
	}})
}

func TestVMGroupProvisioningRefreshesExternalDrift(t *testing.T) {
	var drifted, deleted atomic.Bool
	group := testVMGroup()
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"name": group.Name})
		},
		"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			current := testVMGroup()
			if drifted.Load() {
				current.Status = "pending"
				current.Tags = []client.Tag{{Key: "env", Value: "production"}}
				current.AttachedVolumes = []string{"scratch", "logs", "data"}
			}
			writeData(w, http.StatusOK, current)
		},
		"DELETE /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			writeData(w, http.StatusOK, map[string]string{"deleted": group.Name})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: vmGroupConfig(environment), Check: resource.TestCheckResourceAttr("asc_vm_group.test", "status", "active")},
		{
			PreConfig: func() { drifted.Store(true) },
			// Drifted tags require replacement on groups.
			RefreshState: true, ExpectNonEmptyPlan: true,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_vm_group.test", "status", "pending"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "tags.env", "production"),
				resource.TestCheckResourceAttr("asc_vm_group.test", "attached_volumes.2", "scratch"),
			),
		},
	}})
}

func TestVMGroupProvisioningRecoversFromOutOfBandDeletion(t *testing.T) {
	var removed atomic.Bool
	group := testVMGroup()
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"name": group.Name})
		},
		"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			if removed.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, group)
		},
		"DELETE /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, map[string]string{"deleted": group.Name})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: vmGroupConfig(environment), Check: resource.TestCheckResourceAttr("asc_vm_group.test", "status", "active")},
		{
			PreConfig:    func() { removed.Store(true) },
			RefreshState: true, ExpectNonEmptyPlan: true,
			Check: func(state *terraform.State) error {
				if _, exists := state.RootModule().Resources["asc_vm_group.test"]; exists {
					return fmt.Errorf("asc_vm_group.test is still in state after the gateway reported it gone")
				}
				return nil
			},
		},
	}})
}

func TestVMGroupProvisioningSurfacesTerminalError(t *testing.T) {
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusCreated, map[string]string{"name": "test-group"})
		},
		"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			failed := testVMGroup()
			failed.Status = "error"
			failed.LastError = "no capacity for the requested SKU"
			writeData(w, http.StatusOK, failed)
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config:      vmGroupConfig(environment),
		ExpectError: regexp.MustCompile("no capacity for the requested SKU"),
	}}})
}

func TestVMGroupProvisioningRejectsInvertedReplicaRange(t *testing.T) {
	environment := newTestEnvironment(t, nil)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
resource "asc_vm_group" "test" {
  name = "test-group"
  sku_id = "gp-2"
  vpc_id = "00000000-0000-4000-8000-000000000001"
  autoscaling = { min_replicas = 6, max_replicas = 2 }
}`,
		ExpectError: regexp.MustCompile("min_replicas"),
	}}})
}

func TestVMGroupReplacementReusesTheReleasedName(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	present := false

	currentGroup := func() client.VMGroup {
		skuID := "gp-2"
		if posts >= 2 {
			skuID = "gp-4"
		}
		return client.VMGroup{
			URN: "asc:vm_group:test-group", Name: "test-group",
			SKU: client.SKU{ID: skuID, Name: "general-purpose"}, Status: "active",
			AutoscalingPolicy: client.AutoscalingPolicy{MinReplicas: 2, MaxReplicas: 6},
		}
	}

	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			// The previous deletion acknowledged release of the name reservation.
			if present {
				w.WriteHeader(http.StatusConflict)
				return
			}
			posts++
			present = true
			writeData(w, http.StatusCreated, map[string]string{"created": "test-group"})
		},
		"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if !present {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeData(w, http.StatusOK, currentGroup())
		},
		"DELETE /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			present = false
			mu.Unlock()
			writeData(w, http.StatusOK, map[string]string{"deleted": "test-group"})
		},
	})

	config := func(skuID string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_vm_group" "test" {
  name = "test-group"
  sku_id = %q
  vpc_id = "00000000-0000-4000-8000-000000000001"
  autoscaling = { min_replicas = 2, max_replicas = 6 }
}`, skuID)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config("gp-2"), Check: resource.TestCheckResourceAttr("asc_vm_group.test", "sku.id", "gp-2")},
		{
			Config: config("gp-4"),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("asc_vm_group.test", plancheck.ResourceActionDestroyBeforeCreate),
			}},
			Check: resource.TestCheckResourceAttr("asc_vm_group.test", "sku.id", "gp-4"),
		},
	}})

	mu.Lock()
	defer mu.Unlock()
	if posts != 2 {
		t.Fatalf("POST count = %d, want 2 (the original and its replacement, with no conflict retry)", posts)
	}
}

func TestVMGroupReportsATakenNameAtOnce(t *testing.T) {
	var posts atomic.Int32
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups": func(w http.ResponseWriter, _ *http.Request) {
			posts.Add(1)
			w.WriteHeader(http.StatusConflict)
		},
		"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
resource "asc_vm_group" "test" {
  name = "test-group"
  sku_id = "gp-2"
  vpc_id = "00000000-0000-4000-8000-000000000001"
  autoscaling = { min_replicas = 2, max_replicas = 6 }
}`,
		ExpectError: regexp.MustCompile("already in use for this tenant"),
	}}})

	if got := posts.Load(); got != 1 {
		t.Fatalf("POST count = %d, want 1: the conflict must not be retried", got)
	}
}
