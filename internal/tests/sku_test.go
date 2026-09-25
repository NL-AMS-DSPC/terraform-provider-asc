package tests

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

func TestAccSKUsDataSource(t *testing.T) {
	catalogue := []client.SKU{
		{
			ID: "00000000-0000-4000-8000-000000000010", Name: "gp-small", Family: "general-purpose",
			Threads: 2, Cores: 1, MemoryInMB: 2048, StorageInGB: 40, StorageType: "standard",
		},
		{
			ID: "00000000-0000-4000-8000-000000000011", Name: "gpu-large", Family: "compute-optimized",
			Threads: 2, Cores: 16, MemoryInMB: 65536, StorageInGB: 400, StorageType: "nvme",
			GPUCount: 2, GPUType: "nvidia-a100",
		},
	}

	// Equal-weight SKUs can change order in the gateway response.
	var (
		mu         sync.Mutex
		reshuffled atomic.Bool
		requests   = map[string]int{}
	)
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/skus": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requests[r.Method+" "+r.URL.Path]++
			mu.Unlock()
			served := catalogue
			if reshuffled.Load() {
				served = []client.SKU{catalogue[1], catalogue[0]}
			}
			writeData(w, http.StatusOK, served)
		},
	})

	// Consume the first entry so unstable ordering appears as an output change.
	config := environment.providerConfig() + `
data "asc_skus" "test" {}

output "first_sku" {
  value = data.asc_skus.test.skus[0].name
}
`

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.#", "2"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.id", catalogue[0].ID),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.name", "gp-small"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.family", "general-purpose"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.threads", "2"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.cores", "1"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.memory_in_mb", "2048"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.storage_in_gb", "40"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.storage_type", "standard"),
				// A SKU without GPUs must still land as a known zero/empty, not null.
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.gpu_count", "0"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.gpu_type", ""),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.1.name", "gpu-large"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.1.storage_type", "nvme"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.1.gpu_count", "2"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.1.gpu_type", "nvidia-a100"),
			),
		},
		{
			// Reverse the response while preserving the expected state and output.
			PreConfig: func() { reshuffled.Store(true) },
			Config:    config,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.#", "2"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.id", catalogue[0].ID),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.0.name", "gp-small"),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.1.id", catalogue[1].ID),
				resource.TestCheckResourceAttr("data.asc_skus.test", "skus.1.name", "gpu-large"),
				resource.TestCheckOutput("first_sku", "gp-small"),
			),
		},
	}})

	if !reshuffled.Load() {
		t.Fatal("the reshuffled catalogue step never ran")
	}

	mu.Lock()
	defer mu.Unlock()
	if requests["GET /api/infra-gateway/skus"] == 0 {
		t.Fatal("the data source never called GET /api/infra-gateway/skus")
	}
	for route := range requests {
		if route != "GET /api/infra-gateway/skus" {
			t.Errorf("unexpected outbound route %q", route)
		}
	}
}
