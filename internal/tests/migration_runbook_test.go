package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationRunbookBackupAndRestore(t *testing.T) {
	environment := newTestEnvironment(t, migrationCases()["virtual_machine"])
	dir := seedMigrationWorkspace(t, environment, "virtual_machine")
	backup, err := terraformRun(t, dir, "state", "pull")
	if err != nil {
		t.Fatalf("state pull: %v\n%s", err, backup)
	}
	var state struct {
		Serial    int               `json:"serial"`
		Lineage   string            `json:"lineage"`
		Resources []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal([]byte(backup), &state); err != nil || state.Lineage == "" || state.Serial == 0 || len(state.Resources) != 1 {
		t.Fatalf("invalid backup: %v\n%s", err, backup)
	}
	for _, args := range [][]string{{"state", "list"}, {"state", "show", "asc_virtual_machine.test"}, {"providers"}} {
		if output, err := terraformRun(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}

	restored := t.TempDir()
	config, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"main.tf": config, "backup.json": []byte(backup)} {
		if err := os.WriteFile(filepath.Join(restored, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := terraformRun(t, restored, "state", "push", "backup.json"); err != nil {
		t.Fatalf("restore backup: %v\n%s", err, output)
	}
	for _, args := range [][]string{
		{"plan", "-refresh-only", "-input=false", "-lock-timeout=120s", "-out=refresh.tfplan"},
		{"show", "-json", "refresh.tfplan"},
	} {
		if output, err := terraformRun(t, restored, args...); err != nil {
			t.Fatalf("refresh-only inspection %v: %v\n%s", args, err, output)
		}
	}
	changes, output, err := terraformPlan(t, restored)
	if err != nil || len(changes) != 0 {
		t.Fatalf("restored state must plan no changes: %v, %v\n%s", changes, err, output)
	}
	if len(environment.recordedRoutes()) == 0 {
		t.Fatal("restored state was never refreshed against the gateway")
	}
}

func TestMigrationRunbookNoOpApply(t *testing.T) {
	for _, saved := range []bool{true, false} {
		name := "automatic-plan"
		if saved {
			name = "saved-plan"
		}
		t.Run(name, func(t *testing.T) {
			environment := newTestEnvironment(t, migrationCases()["virtual_machine"])
			dir := seedMigrationWorkspace(t, environment, "virtual_machine")
			changes, output, err := terraformPlan(t, dir)
			if err != nil || len(changes) != 0 {
				t.Fatalf("refuse to apply a changing or failed plan: %v, %v\n%s", changes, err, output)
			}
			args := []string{"apply", "-input=false", "-no-color"}
			if saved {
				args = append(args, "tfplan")
			} else {
				args = append(args, "-auto-approve")
			}
			output, err = terraformRun(t, dir, args...)
			if err != nil || !strings.Contains(output, "0 added, 0 changed, 0 destroyed") {
				t.Fatalf("no-op apply: %v\n%s", err, output)
			}
			changes, output, err = terraformPlan(t, dir)
			if err != nil || len(changes) != 0 {
				t.Fatalf("plan after apply: %v, %v\n%s", changes, err, output)
			}
			for _, route := range environment.recordedRoutes() {
				if !strings.HasPrefix(route, "GET ") {
					t.Errorf("no-op apply mutated the gateway: %s", route)
				}
			}
		})
	}
}
