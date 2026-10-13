package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The fake gateway enforces the create contract of ASC main (commit 12e151d01): a VM needs
// a valid bootDisk and a group needs a top-level image and diskSizeGB. A create that breaks
// it gets the 400 the real gateway returns, so a regression fails the acceptance suite
// instead of passing against a fake that accepts anything.

type createBodies struct {
	mu     sync.Mutex
	bodies map[string][]map[string]any
}

func (c *createBodies) add(path string, body map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bodies == nil {
		c.bodies = map[string][]map[string]any{}
	}
	c.bodies[path] = append(c.bodies[path], body)
}

func (c *createBodies) last(path string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bodies := c.bodies[path]
	if len(bodies) == 0 {
		return nil, false
	}
	return bodies[len(bodies)-1], true
}

// bootDiskProblems mirrors the boot-disk validator in ASC's infra-gateway.
func bootDiskProblems(body map[string]any) map[string]string {
	disk, ok := body["bootDisk"].(map[string]any)
	if !ok {
		return map[string]string{"BootDisk": "is required"}
	}
	mode, _ := disk["attachMode"].(string)
	size, _ := disk["diskSizeGB"].(float64)
	image, _ := disk["image"].(string)
	existing, _ := disk["existingDiskName"].(string)
	valid := false
	switch strings.ToLower(mode) {
	case "create":
		valid = size > 0 && image != "" && existing == ""
	case "attach":
		valid = existing != "" && size == 0 && image == ""
	}
	if !valid {
		return map[string]string{"BootDisk": "Boot disk is not valid"}
	}
	return nil
}

// vmGroupProblems mirrors the required image and diskSizeGB on a group create.
func vmGroupProblems(body map[string]any) map[string]string {
	problems := map[string]string{}
	if image, _ := body["image"].(string); image == "" {
		problems["Image"] = "is required"
	}
	if size, _ := body["diskSizeGB"].(float64); size <= 0 {
		problems["DiskSizeGB"] = "must be greater than 0"
	}
	return problems
}

// checkCreateContract records VM and group create bodies and answers 400 for invalid ones.
// It reports whether it rejected the request.
func checkCreateContract(w http.ResponseWriter, r *http.Request, seen *createBodies) bool {
	if r.Method != http.MethodPost || (r.URL.Path != "/api/infra-gateway/vms" && r.URL.Path != "/api/infra-gateway/vm-groups") {
		return false
	}
	raw, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return true
	}
	seen.add(r.URL.Path, body)

	problems := bootDiskProblems(body)
	if r.URL.Path == "/api/infra-gateway/vm-groups" {
		problems = vmGroupProblems(body)
	}
	if len(problems) == 0 {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(problems)
	return true
}

func TestFakeGatewayEnforcesTheBootDiskContract(t *testing.T) {
	valid := map[string]any{"bootDisk": map[string]any{"attachMode": "create", "diskSizeGB": float64(32), "image": "ubuntu"}}
	tests := []struct {
		name string
		body map[string]any
		ok   bool
	}{
		{"create with image and size", valid, true},
		{"no bootDisk", map[string]any{"image": "ubuntu"}, false},
		{"create without image", map[string]any{"bootDisk": map[string]any{"attachMode": "create", "diskSizeGB": float64(32)}}, false},
		{"create without size", map[string]any{"bootDisk": map[string]any{"attachMode": "create", "image": "ubuntu"}}, false},
		{"create with an existing disk", map[string]any{"bootDisk": map[string]any{"attachMode": "create", "diskSizeGB": float64(32), "image": "ubuntu", "existingDiskName": "d"}}, false},
		{"unknown mode", map[string]any{"bootDisk": map[string]any{"attachMode": "clone", "diskSizeGB": float64(32), "image": "ubuntu"}}, false},
		{"attach an existing disk", map[string]any{"bootDisk": map[string]any{"attachMode": "attach", "existingDiskName": "d"}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := len(bootDiskProblems(test.body)) == 0; got != test.ok {
				t.Errorf("accepted = %t, want %t", got, test.ok)
			}
		})
	}
	if len(vmGroupProblems(map[string]any{"image": "ubuntu", "diskSizeGB": float64(32)})) != 0 {
		t.Error("a group with image and size was rejected")
	}
	if len(vmGroupProblems(map[string]any{"image": "ubuntu"})) == 0 || len(vmGroupProblems(map[string]any{"diskSizeGB": float64(32)})) == 0 {
		t.Error("a group missing image or size was accepted")
	}
}

// checkCreateBody asserts on the last create body the fake saw at path. Keys may be
// dot-separated to reach nested objects, and a nil want value asserts the key is absent.
func checkCreateBody(environment testEnvironment, path string, want map[string]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		body, ok := environment.lastCreate(path)
		if !ok {
			return fmt.Errorf("the fake gateway saw no create at %s", path)
		}
		for key, expected := range want {
			var current any = body
			for _, part := range strings.Split(key, ".") {
				object, isObject := current.(map[string]any)
				if !isObject {
					current = nil
					break
				}
				current = object[part]
			}
			if current != expected {
				return fmt.Errorf("create body %s = %v, want %v", key, current, expected)
			}
		}
		return nil
	}
}
