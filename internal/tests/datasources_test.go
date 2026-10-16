package tests

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

func TestAccVPCDataSource(t *testing.T) {
	vpc := client.VPC{
		ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
		CIDR: "10.0.0.0/24", Status: "active", Tags: []client.Tag{{Key: "env", Value: "test"}},
		Subnets: []client.Subnet{{
			ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
			VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.0/25", Status: "active",
			Tags: []client.Tag{{Key: "tier", Value: "data"}},
		}},
	}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) { writeData(w, http.StatusOK, vpc) },
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_vpc" "test" {
  name = "test-vpc"
}`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.asc_vpc.test", "id", vpc.ID),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "urn", vpc.URN),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "name", "test-vpc"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "cidr", "10.0.0.0/24"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "status", "active"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "last_error", ""),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "tags.env", "test"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.#", "1"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.0.id", vpc.Subnets[0].ID),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.0.urn", vpc.Subnets[0].URN),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.0.name", "bootstrap"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.0.vpc_id", vpc.ID),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.0.cidr", "10.0.0.0/25"),
			resource.TestCheckNoResourceAttr("data.asc_vpc.test", "subnets.0.type"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.0.status", "active"),
			resource.TestCheckResourceAttr("data.asc_vpc.test", "subnets.0.tags.tier", "data"),
		),
	}}})
}

func TestAccVPCDataSourceRejectsUnknownVPC(t *testing.T) {
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vpcs/ghost": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_vpc" "test" {
  name = "ghost"
}`,
		ExpectError: regexp.MustCompile(`VPC not found`),
	}}})
}

func TestAccSubnetDataSource(t *testing.T) {
	subnet := client.Subnet{
		ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:test-subnet", Name: "test-subnet",
		VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.128/25", Status: "active",
		Tags: []client.Tag{{Key: "tier", Value: "edge"}},
	}
	// infra-gateway has no single-subnet GET route, so the data source reads the
	// VPC's listing and matches on the name. The fixture serves a second subnet
	// to prove the match is exact rather than positional.
	other := client.Subnet{
		ID: "00000000-0000-4000-8000-000000000003", URN: "asc:subnet:other", Name: "other",
		VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.0/25", Status: "active",
	}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vpcs/test-vpc/subnets": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, []client.Subnet{other, subnet})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_subnet" "test" {
  vpc_name = "test-vpc"
  name     = "test-subnet"
}`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.asc_subnet.test", "id", subnet.ID),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "urn", subnet.URN),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "name", "test-subnet"),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "vpc_name", "test-vpc"),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "vpc_id", subnet.VPCID),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "cidr", "10.0.0.128/25"),
			resource.TestCheckNoResourceAttr("data.asc_subnet.test", "type"),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "status", "active"),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "last_error", ""),
			resource.TestCheckResourceAttr("data.asc_subnet.test", "tags.tier", "edge"),
		),
	}}})
}

func TestAccSubnetDataSourceRejectsUnknownVPC(t *testing.T) {
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vpcs/ghost/subnets": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_subnet" "test" {
  vpc_name = "ghost"
  name     = "test-subnet"
}`,
		ExpectError: regexp.MustCompile(`Subnet not found`),
	}}})
}

func TestAccSubnetDataSourceRejectsUnknownSubnet(t *testing.T) {
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vpcs/test-vpc/subnets": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, []client.Subnet{})
		},
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_subnet" "test" {
  vpc_name = "test-vpc"
  name     = "ghost"
}`,
		ExpectError: regexp.MustCompile(`Subnet not found`),
	}}})
}

func TestAccVirtualMachineDataSource(t *testing.T) {
	vm := client.VM{
		URN: "asc:vm:test-vm", Name: "test-vm", VPCID: "00000000-0000-4000-8000-000000000001",
		SKU: client.SKU{
			ID: "00000000-0000-4000-8000-000000000010", Name: "gp-small", Family: "general-purpose",
			Threads: 2, Cores: 4, MemoryInMB: 8192, StorageInGB: 40, StorageType: "nvme",
			GPUCount: 1, GPUType: "nvidia-a100",
		},
		Status: "active", Tags: []client.Tag{{Key: "env", Value: "test"}},
		// Served out of order on purpose: infra-gateway builds this list by
		// ranging over a Go map, so state has to come out sorted regardless.
		AttachedVolumes: []string{"vol-c", "vol-a", "vol-b"},
		OS:              client.OS{ID: "ubuntu-24.04", Family: "linux", Distribution: "ubuntu", Release: "24.04", DisplayName: "Ubuntu 24.04"},
	}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) { writeData(w, http.StatusOK, vm) },
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_virtual_machine" "test" {
  name = "test-vm"
}`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "urn", vm.URN),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "name", "test-vm"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "vpc_id", vm.VPCID),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "status", "active"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "last_error", ""),
			resource.TestCheckNoResourceAttr("data.asc_virtual_machine.test", "enable_logging"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "tags.env", "test"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.id", vm.SKU.ID),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.name", "gp-small"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.family", "general-purpose"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.threads", "2"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.cores", "4"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.memory_in_mb", "8192"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.storage_in_gb", "40"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.storage_type", "nvme"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.gpu_count", "1"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "sku.gpu_type", "nvidia-a100"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "os.id", "ubuntu-24.04"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "os.family", "linux"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "os.distribution", "ubuntu"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "os.release", "24.04"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "os.display_name", "Ubuntu 24.04"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "attached_volumes.#", "3"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "attached_volumes.0", "vol-a"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "attached_volumes.1", "vol-b"),
			resource.TestCheckResourceAttr("data.asc_virtual_machine.test", "attached_volumes.2", "vol-c"),
		),
	}}})
}

func TestAccVirtualMachineDataSourceRejectsUnknownVirtualMachine(t *testing.T) {
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vms/ghost": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
	})

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_virtual_machine" "test" {
  name = "ghost"
}`,
		ExpectError: regexp.MustCompile(`Virtual machine not found`),
	}}})
}
