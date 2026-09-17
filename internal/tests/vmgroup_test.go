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

// testVMGroup is the group the gateway reports. Note what it does not carry:
// no vpcID, no image and no customization, so the provider can only keep those
// from the configuration.
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
		"POST /api/infra-gateway/vm-groups/": func(w http.ResponseWriter, _ *http.Request) {
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
			// An import reads the gateway, which reports neither the VPC, the
			// image nor the customization, so these three cannot be recovered.
			ImportStateVerifyIgnore: []string{"vpc_id", "image", "customization"},
		},
	}})
}

func TestVMGroupProvisioningRefreshesExternalDrift(t *testing.T) {
	var drifted, deleted atomic.Bool
	group := testVMGroup()
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups/": func(w http.ResponseWriter, _ *http.Request) {
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
			// A refresh has to record what the gateway reports. The tag moved
			// out of band and tags force a replacement, so the follow-up plan is
			// deliberately not empty.
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
		"POST /api/infra-gateway/vm-groups/": func(w http.ResponseWriter, _ *http.Request) {
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
			PreConfig: func() { removed.Store(true) },
			// A 404 is authoritative absence, so the refresh drops the group
			// from state and the next plan recreates it.
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
		"POST /api/infra-gateway/vm-groups/": func(w http.ResponseWriter, _ *http.Request) {
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

// TestVMGroupReplacementReusesTheReleasedName covers the whole reason
// asc_vm_group has to work: every attribute forces replacement, so a change is
// a delete followed by a create under the same name. infra-gateway releases the
// name when it acknowledges the group's deletion, so the create that follows
// succeeds and no conflict arises.
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
		"POST /api/infra-gateway/vm-groups/": func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			// The name is free: the previous generation's deletion was
			// acknowledged, which is what releases it in vm_names_in_use.
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

// TestVMGroupReportsATakenNameAtOnce pins the fail-fast half. A 409 on this
// route is always a name that is taken now, so the provider reports it straight
// away instead of polling until the lifecycle timeout and reporting a deadline.
func TestVMGroupReportsATakenNameAtOnce(t *testing.T) {
	var posts atomic.Int32
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vm-groups/": func(w http.ResponseWriter, _ *http.Request) {
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
