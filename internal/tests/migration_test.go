package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

// Migration tests run Terraform against legacy state and a mock gateway. State fixtures
// retain old field names; configuration fixtures use the new schema with equivalent
// inputs. See docs/compatibility.md.

var (
	devOverrideOnce sync.Once
	devOverrideCLI  string
	devOverrideDir  string
	devOverrideErr  error
)

// providerDevOverride builds one provider per package run without requiring a registry
// install.
func providerDevOverride(t *testing.T) string {
	t.Helper()
	devOverrideOnce.Do(func() {
		// Shared across tests and removed by TestMain.
		dir, err := os.MkdirTemp("", "asc-dev-override")
		if err != nil {
			devOverrideErr = err
			return
		}
		devOverrideDir = dir
		build := exec.Command("go", "build", "-o", filepath.Join(dir, "terraform-provider-asc"), ".")
		build.Dir = "../.."
		if output, err := build.CombinedOutput(); err != nil {
			devOverrideErr = fmt.Errorf("build provider: %w\n%s", err, output)
			return
		}
		config := filepath.Join(dir, "terraform.tfrc")
		body := fmt.Sprintf(`provider_installation {
  dev_overrides {
    "nl-ams-dspc/asc" = %q
  }
  direct {}
}
`, dir)
		if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
			devOverrideErr = err
			return
		}
		devOverrideCLI = config
	})
	if devOverrideErr != nil {
		t.Fatalf("prepare dev override: %v", devOverrideErr)
	}
	return devOverrideCLI
}

// planChange is one resource action from the saved plan, or from the streamed
// diagnostics when planning fails before a complete plan can be saved.
type planChange struct {
	Resource string
	Action   string
}

// terraformBinary shares the acceptance suite's explicit binary override and fails if no
// CLI is available.
func terraformBinary(t *testing.T) string {
	t.Helper()
	if path := strings.TrimSpace(os.Getenv("TF_ACC_TERRAFORM_PATH")); path != "" {
		return path
	}
	path, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("the migration tier needs the terraform CLI: put it on PATH or set TF_ACC_TERRAFORM_PATH (%v)", err)
	}
	return path
}

// terraformPlan reads actions from the saved plan, retaining streamed diagnostics when
// planning fails.
func terraformPlan(t *testing.T, dir string, options ...string) ([]planChange, string, error) {
	t.Helper()
	args := append([]string{"plan", "-json", "-input=false", "-lock=false", "-out=tfplan"}, options...)
	command := exec.Command(terraformBinary(t), args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+providerDevOverride(t), "TF_IN_AUTOMATION=1")
	output, err := command.CombinedOutput()
	if err == nil {
		show := exec.Command(terraformBinary(t), "show", "-json", "tfplan")
		show.Dir = dir
		show.Env = command.Env
		planJSON, showErr := show.Output()
		if showErr != nil {
			t.Fatalf("terraform show saved plan: %v", showErr)
		}
		var plan struct {
			Changes []struct {
				Address string `json:"address"`
				Change  struct {
					Actions []string `json:"actions"`
				} `json:"change"`
			} `json:"resource_changes"`
		}
		if err := json.Unmarshal(planJSON, &plan); err != nil {
			t.Fatalf("decode saved plan: %v\n%s", err, planJSON)
		}
		var changes []planChange
		for _, change := range plan.Changes {
			action := strings.Join(change.Change.Actions, "_then_")
			if action != "no-op" {
				changes = append(changes, planChange{Resource: change.Address, Action: action})
			}
		}
		return changes, string(output), nil
	}

	var changes []planChange
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var message struct {
			Type   string `json:"type"`
			Change struct {
				Resource struct {
					Addr string `json:"addr"`
				} `json:"resource"`
				Action string `json:"action"`
			} `json:"change"`
		}
		if json.Unmarshal([]byte(line), &message) != nil || message.Type != "planned_change" {
			continue
		}
		changes = append(changes, planChange{Resource: message.Change.Resource.Addr, Action: message.Change.Action})
	}
	return changes, string(output), err
}

// seedMigrationWorkspace combines legacy state and configuration fixtures with the mock
// provider endpoints.
func seedMigrationWorkspace(t *testing.T, environment testEnvironment, name string) string {
	t.Helper()
	// An explicit source prevents Terraform defaulting to hashicorp/asc.
	header := `terraform {
  required_providers {
    asc = {
      source = "nl-ams-dspc/asc"
    }
  }
}
` + environment.providerConfig()
	return seedWorkspace(t, name, header)
}

func seedWorkspace(t *testing.T, name, header string) string {
	t.Helper()
	dir := t.TempDir()

	state, err := os.ReadFile(filepath.Join("fixtures", name+".tfstate"))
	if err != nil {
		t.Fatalf("read state fixture: %v", err)
	}
	config, err := os.ReadFile(filepath.Join("fixtures", name+".tf"))
	if err != nil {
		t.Fatalf("read config fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), state, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(header+string(config)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}

// Reject replacement in both streamed and saved-plan action formats.
func assertNoDestructivePlan(t testingT, changes []planChange, output string) {
	t.Helper()
	for _, change := range changes {
		switch change.Action {
		case "create", "delete", "replace", "delete_then_create", "create_then_delete":
			t.Errorf("plan would %s %s; legacy state must never be planned destructively\n%s", change.Action, change.Resource, output)
		}
	}
}

// migrationCases serves unchanged objects for the four infrastructure fixtures.
func migrationCases() map[string]map[string]http.HandlerFunc {
	vpc := client.VPC{
		ID: "00000000-0000-4000-8000-000000000001", URN: "asc:vpc:test-vpc", Name: "test-vpc",
		CIDR: "10.0.0.0/24", Status: "active", Tags: []client.Tag{{Key: "env", Value: "test"}},
		Subnets: []client.Subnet{{
			ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:bootstrap", Name: "bootstrap",
			VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.0/25", Type: "private", Status: "active",
		}},
	}
	subnet := client.Subnet{
		ID: "00000000-0000-4000-8000-000000000002", URN: "asc:subnet:test-subnet", Name: "test-subnet",
		VPCID: "00000000-0000-4000-8000-000000000001", CIDR: "10.0.0.128/25", Type: "public", Status: "active",
	}
	vm := client.VM{
		URN: "asc:vm:test-vm", Name: "test-vm", VPCID: "00000000-0000-4000-8000-000000000001",
		SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
		OS:   client.OS{ID: "ubuntu-24.04", Family: "linux", Distribution: "ubuntu", Release: "24.04", DisplayName: "Ubuntu 24.04"},
		Tags: []client.Tag{{Key: "env", Value: "test"}}, EnableLogging: true,
	}
	group := testVMGroup()

	return map[string]map[string]http.HandlerFunc{
		"vpc": {
			"GET /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) { writeData(w, http.StatusOK, vpc) },
		},
		"subnet": {
			"GET /api/infra-gateway/vpcs/test-vpc/subnets": func(w http.ResponseWriter, _ *http.Request) {
				writeData(w, http.StatusOK, []client.Subnet{subnet})
			},
		},
		"virtual_machine": {
			"GET /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) { writeData(w, http.StatusOK, vm) },
		},
		"vm_group": {
			"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
				writeData(w, http.StatusOK, group)
			},
		},
	}
}

func TestMigrationFromLegacySameAddressPlansNothing(t *testing.T) {
	for name, handlers := range migrationCases() {
		t.Run(name, func(t *testing.T) {
			environment := newTestEnvironment(t, handlers)
			dir := seedMigrationWorkspace(t, environment, name)

			changes, output, err := terraformPlan(t, dir)
			if err != nil {
				t.Fatalf("terraform plan: %v\n%s", err, output)
			}
			assertNoDestructivePlan(t, changes, output)
			if len(changes) != 0 {
				t.Errorf("plan proposed %v, want no changes\n%s", changes, output)
			}
			if routes := environment.recordedRoutes(); len(routes) == 0 {
				t.Error("the gateway saw no request, so the plan never refreshed the legacy state")
			}
		})
	}
}

func TestMigrationAssertionDetectsAReplacement(t *testing.T) {
	group := testVMGroup()
	group.SKU = client.SKU{ID: "gp-4", Name: "general-purpose"}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vm-groups/test-group": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, group)
		},
	})

	dir := seedMigrationWorkspace(t, environment, "vm_group")
	changes, output, err := terraformPlan(t, dir)
	if err != nil {
		t.Fatalf("terraform plan: %v\n%s", err, output)
	}

	capture := &captureT{}
	assertNoDestructivePlan(capture, changes, output)
	if len(capture.failures) == 0 {
		t.Fatalf("assertNoDestructivePlan saw no destructive action in %v; the migration guard cannot fail\n%s", changes, output)
	}
	if !strings.Contains(capture.failures[0], "asc_vm_group.test") {
		t.Errorf("failure %q does not name the replaced resource", capture.failures[0])
	}
}

func TestMigrationFromReleasedProviderIsNotAnUpgrade(t *testing.T) {
	dir := seedWorkspace(t, "released_dspc", "")
	changes, output, err := terraformPlan(t, dir)
	if err == nil {
		t.Fatalf("plan succeeded; it must fail for want of the dspc provider\n%s", output)
	}
	// Terraform lower-cases a source address, so the NL-AMS-DSPC the release was
	// published under comes back as nl-ams-dspc.
	if !strings.Contains(output, "nl-ams-dspc/dspc") {
		t.Errorf("plan error does not name the provider that wrote the state\n%s", output)
	}
	if len(changes) != 0 {
		t.Errorf("plan proposed %v; no provider should have planned dspc_virtual_machine\n%s", changes, output)
	}
}

// terraformRun uses the same development override as terraformPlan.
func terraformRun(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command(terraformBinary(t), args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+providerDevOverride(t), "TF_IN_AUTOMATION=1")
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestMigrationVanishedObjectPlansACreateNotADestroy(t *testing.T) {
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})

	dir := seedMigrationWorkspace(t, environment, "vpc")
	changes, output, err := terraformPlan(t, dir)
	if err != nil {
		t.Fatalf("terraform plan: %v\n%s", err, output)
	}
	if len(changes) != 1 || changes[0].Action != "create" || changes[0].Resource != "asc_vpc.test" {
		t.Fatalf("plan proposed %v, want exactly one create of asc_vpc.test\n%s", changes, output)
	}
}

func TestMigrationWrongTenantFailsLoudly(t *testing.T) {
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vpcs/test-vpc": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, client.VPC{Name: "test-vpc"})
		},
	})

	dir := seedMigrationWorkspace(t, environment, "vpc")
	// The harness rejects any tenant but test-tenant, so overriding the
	// namespace reproduces a real cross-tenant misconfiguration end to end.
	config, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	body := strings.Replace(string(config), `namespace = "test-tenant"`, `namespace = "other-tenant"`, 1)
	if body == string(config) {
		t.Fatal("the namespace was not overridden; the provider config template changed")
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	changes, output, err := terraformPlan(t, dir)
	if err == nil {
		t.Fatalf("plan succeeded against the wrong tenant; it must fail\n%s", output)
	}
	if len(changes) != 0 {
		t.Errorf("plan proposed %v; a refusal to read must not become a planned change\n%s", changes, output)
	}
}

func TestMigrationRetiredEntryFailsUntilRemoved(t *testing.T) {
	environment := newTestEnvironment(t, nil)
	dir := seedMigrationWorkspace(t, environment, "retired_entry")

	changes, output, err := terraformPlan(t, dir)
	if err == nil {
		t.Fatalf("plan succeeded with a retired entry in state; it must fail\n%s", output)
	}
	if !strings.Contains(output, "asc_group_member") {
		t.Errorf("the plan error does not name the retired type\n%s", output)
	}
	if len(changes) != 0 {
		t.Errorf("plan proposed %v; a retired entry must not be planned at all\n%s", changes, output)
	}

	if output, err := terraformRun(t, dir, "state", "rm", "asc_group_member.platform_alice"); err != nil {
		t.Fatalf("state rm: %v\n%s", err, output)
	}
	if _, output, err := terraformPlan(t, dir); err != nil {
		t.Fatalf("plan still fails after removing the retired entry: %v\n%s", err, output)
	}
}

func TestMigrationImportAdoptsALiveMachine(t *testing.T) {
	vm := client.VM{
		URN: "asc:vm:test-vm", Name: "test-vm", VPCID: "00000000-0000-4000-8000-000000000001",
		SKU: client.SKU{ID: "gp-2", Name: "general-purpose"}, Status: "active",
		OS:   client.OS{ID: "ubuntu-24.04", Family: "linux", Distribution: "ubuntu", Release: "24.04", DisplayName: "Ubuntu 24.04"},
		Tags: []client.Tag{{Key: "env", Value: "test"}}, EnableLogging: true,
	}
	environment := newTestEnvironment(t, map[string]http.HandlerFunc{
		"GET /api/infra-gateway/vms/test-vm": func(w http.ResponseWriter, _ *http.Request) {
			writeData(w, http.StatusOK, vm)
		},
	})

	dir := seedMigrationWorkspace(t, environment, "import_vm")
	if output, err := terraformRun(t, dir, "state", "rm", "dspc_virtual_machine.test"); err != nil {
		t.Fatalf("remove released-provider state: %v\n%s", err, output)
	}
	if output, err := terraformRun(t, dir, "import", "-input=false", "-lock=false", "asc_virtual_machine.test", "test-vm"); err != nil {
		t.Fatalf("terraform import: %v\n%s", err, output)
	}

	// A successful import must also produce an empty plan.
	changes, output, err := terraformPlan(t, dir)
	if err != nil {
		t.Fatalf("plan after import: %v\n%s", err, output)
	}
	assertNoDestructivePlan(t, changes, output)
	if len(changes) != 0 {
		t.Errorf("plan after import proposed %v, want no changes\n%s", changes, output)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if devOverrideDir != "" {
		if err := os.RemoveAll(devOverrideDir); err != nil {
			fmt.Fprintf(os.Stderr, "remove provider test build: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

func TestMigrationWithoutRefreshDetectsGroupReplacement(t *testing.T) {
	environment := newTestEnvironment(t, migrationCases()["vm_group"])
	dir := seedMigrationWorkspace(t, environment, "vm_group")
	changes, output, err := terraformPlan(t, dir, "-refresh=false")
	if err != nil {
		t.Fatalf("plan without refresh: %v\n%s", err, output)
	}
	capture := &captureT{}
	assertNoDestructivePlan(capture, changes, output)
	if len(capture.failures) == 0 {
		t.Fatalf("stale autoscaling state must trigger replacement: %v\n%s", changes, output)
	}
	if len(environment.recordedRoutes()) != 0 {
		t.Fatal("plan refreshed despite -refresh=false")
	}
}

func TestMigrationLegacyGroupConfigurationRequiresRename(t *testing.T) {
	environment := newTestEnvironment(t, migrationCases()["vm_group"])
	dir := seedMigrationWorkspace(t, environment, "vm_group")
	configPath := filepath.Join(dir, "main.tf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(config), "autoscaling =", "autoscaling_policy =", 1)
	if legacy == string(config) {
		t.Fatal("fixture no longer configures autoscaling")
	}
	if err := os.WriteFile(configPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	changes, output, err := terraformPlan(t, dir)
	if err == nil || !strings.Contains(output, "autoscaling_policy") || len(changes) != 0 {
		t.Fatalf("legacy configuration must fail before planning: %v, %v\n%s", changes, err, output)
	}
}

// bootDiskMigrationCases are the fixtures whose resources gained boot_disk_size_gb after
// their state was written. The state fixtures have no such attribute.
var bootDiskMigrationCases = map[string]string{
	"virtual_machine": "asc_virtual_machine.test",
	"vm_group":        "asc_vm_group.test",
}

// withBootDiskSize adds boot_disk_size_gb to the first resource block of the workspace.
func withBootDiskSize(t *testing.T, dir string, size int) {
	t.Helper()
	configPath := filepath.Join(dir, "main.tf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(config), "  image ", fmt.Sprintf("  boot_disk_size_gb = %d\n  image ", size), 1)
	if updated == string(config) {
		t.Fatal("fixture no longer configures image")
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// State written before boot_disk_size_gb existed must plan no change, and an explicit 32
// (the default) must not plan a replacement either.
func TestMigrationBootDiskSizeAbsentFromStateNeverReplaces(t *testing.T) {
	for name := range bootDiskMigrationCases {
		t.Run(name, func(t *testing.T) {
			environment := newTestEnvironment(t, migrationCases()[name])
			dir := seedMigrationWorkspace(t, environment, name)
			changes, output, err := terraformPlan(t, dir)
			if err != nil {
				t.Fatalf("terraform plan: %v\n%s", err, output)
			}
			assertNoDestructivePlan(t, changes, output)
			if len(changes) != 0 {
				t.Errorf("plan proposed %v, want no changes\n%s", changes, output)
			}
		})
		t.Run(name+"-explicit-default", func(t *testing.T) {
			environment := newTestEnvironment(t, migrationCases()[name])
			dir := seedMigrationWorkspace(t, environment, name)
			withBootDiskSize(t, dir, 32)
			changes, output, err := terraformPlan(t, dir)
			if err != nil {
				t.Fatalf("terraform plan: %v\n%s", err, output)
			}
			assertNoDestructivePlan(t, changes, output)
		})
	}
}

// A size other than the 32 that absent state stands for is a real change to the boot disk.
func TestMigrationBootDiskSizeDifferentFromTheDefaultReplaces(t *testing.T) {
	for name, address := range bootDiskMigrationCases {
		t.Run(name, func(t *testing.T) {
			environment := newTestEnvironment(t, migrationCases()[name])
			dir := seedMigrationWorkspace(t, environment, name)
			withBootDiskSize(t, dir, 64)
			changes, output, err := terraformPlan(t, dir)
			if err != nil {
				t.Fatalf("terraform plan: %v\n%s", err, output)
			}
			if len(changes) != 1 || changes[0].Resource != address || changes[0].Action != "delete_then_create" {
				t.Errorf("plan proposed %v, want a replacement of %s\n%s", changes, address, output)
			}
		})
	}
}
