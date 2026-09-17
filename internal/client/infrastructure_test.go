package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestInfrastructureContract(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Tenant"); got != "tenant" {
			t.Errorf("X-Tenant = %q", got)
		}
		mu.Lock()
		seen[r.Method+" "+r.URL.Path]++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /api/infra-gateway/vpcs/":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			subnet := body["subnets"].([]any)[0].(map[string]any)
			if _, exists := subnet["vpcID"]; exists {
				t.Error("inline subnet must omit vpcID")
			}
			w.WriteHeader(http.StatusCreated)
		case "GET /api/infra-gateway/vpcs/test-vpc":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VPC{Name: "test-vpc"}})
		case "POST /api/infra-gateway/vpcs/test-vpc/subnets/":
			w.WriteHeader(http.StatusCreated)
		case "GET /api/infra-gateway/vpcs/test-vpc/subnets/":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []Subnet{{Name: "test-subnet"}}})
		case "POST /api/infra-gateway/vms/":
			w.WriteHeader(http.StatusCreated)
		case "GET /api/infra-gateway/vms/test-vm":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VM{Name: "test-vm", VPCID: "vpc-id", EnableLogging: true}})
		case "GET /api/infra-gateway/vms/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	runtime := NewRuntime(config)
	ctx := context.Background()

	if _, err := runtime.CreateVPC(ctx, CreateVPCRequest{Name: "test-vpc", Subnets: []CreateSubnetRequest{{Name: "bootstrap", CIDR: "10.0.0.0/25", Type: "private"}}}); err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if _, err := runtime.CreateSubnet(ctx, "test-vpc", CreateSubnetRequest{Name: "test-subnet", CIDR: "10.0.0.128/25", VPCID: "vpc-id", Type: "public"}); err != nil {
		t.Fatalf("CreateSubnet() error = %v", err)
	}
	vm, err := runtime.CreateVM(ctx, CreateVMRequest{Name: "test-vm", SKUID: "sku", VPCID: "vpc-id"})
	if err != nil {
		t.Fatalf("CreateVM() error = %v", err)
	}
	if vm.VPCID != "vpc-id" || !vm.EnableLogging {
		t.Fatalf("VM = %#v", vm)
	}
	if _, err := runtime.GetVM(ctx, "missing"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetVM(missing) error = %v, want ErrResourceNotFound", err)
	}

	for _, request := range []string{
		"POST /api/infra-gateway/vpcs/", "GET /api/infra-gateway/vpcs/test-vpc",
		"POST /api/infra-gateway/vpcs/test-vpc/subnets/", "GET /api/infra-gateway/vpcs/test-vpc/subnets/",
		"POST /api/infra-gateway/vms/", "GET /api/infra-gateway/vms/test-vm",
	} {
		if seen[request] == 0 {
			t.Errorf("request %q was not made", request)
		}
	}
}

// TestGetVMSortsAttachedVolumes pins the provider-side fix for a gateway detail:
// infra-gateway's get_vm projection appends the attachments while ranging over a
// Go map, so two reads of an unchanged machine can return them in different
// orders. The list is positional in Terraform state, so the client sorts it.
func TestGetVMSortsAttachedVolumes(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": VM{
			Name: "test-vm", Status: "active", AttachedVolumes: []string{"vol-c", "vol-a", "vol-b"},
		}})
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	vm, err := NewRuntime(config).GetVM(context.Background(), "test-vm")
	if err != nil {
		t.Fatalf("GetVM() error = %v", err)
	}
	want := []string{"vol-a", "vol-b", "vol-c"}
	if !reflect.DeepEqual(vm.AttachedVolumes, want) {
		t.Fatalf("AttachedVolumes = %v, want %v", vm.AttachedVolumes, want)
	}
}

// TestUpdateTagsPatchesEveryTaggableResource proves the three shipped resources
// reach their own PATCH route with the computed patch and then take their state
// from the authoritative read rather than the PATCH echo.
func TestUpdateTagsPatchesEveryTaggableResource(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	patched := map[string]tagPatchRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}

		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPatch {
			var body tagPatchRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			patched[r.URL.Path] = body
			// The virtual machine and volume handlers echo only the patch
			// entries. Answering every PATCH that way here proves the client
			// never takes state from this body.
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"tags": body.Tags}})
			return
		}

		tags := []Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}}
		if len(patched) > 0 {
			tags = []Tag{{Key: "env", Value: "prod"}}
		}
		switch r.URL.Path {
		case "/api/infra-gateway/vpcs/test-vpc":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VPC{Name: "test-vpc", Status: "active", Tags: tags}})
		case "/api/infra-gateway/vpcs/test-vpc/subnets/":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []Subnet{{Name: "test-subnet", Status: "active", Tags: tags}}})
		case "/api/infra-gateway/vms/test-vm":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VM{Name: "test-vm", Status: "active", Tags: tags}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	runtime := NewRuntime(config)
	runtime.pollInterval = time.Millisecond

	ctx := context.Background()
	current := []Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}}
	desired := []Tag{{Key: "env", Value: "prod"}}

	vpc, err := runtime.UpdateVPCTags(ctx, "test-vpc", current, desired)
	if err != nil || !sameTags(vpc.Tags, desired) {
		t.Fatalf("UpdateVPCTags() = %#v, error = %v", vpc.Tags, err)
	}
	subnet, err := runtime.UpdateSubnetTags(ctx, "test-vpc", "test-subnet", current, desired)
	if err != nil || !sameTags(subnet.Tags, desired) {
		t.Fatalf("UpdateSubnetTags() = %#v, error = %v", subnet.Tags, err)
	}
	vm, err := runtime.UpdateVMTags(ctx, "test-vm", current, desired)
	if err != nil || !sameTags(vm.Tags, desired) {
		t.Fatalf("UpdateVMTags() = %#v, error = %v", vm.Tags, err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []TagPatch{{Key: "env", Value: "prod"}, {Key: "owner"}}
	for _, path := range []string{
		"/api/infra-gateway/vpcs/test-vpc",
		"/api/infra-gateway/vpcs/test-vpc/subnets/test-subnet",
		"/api/infra-gateway/vms/test-vm",
	} {
		if !reflect.DeepEqual(patched[path].Tags, want) {
			t.Errorf("patch to %s = %#v, want %#v", path, patched[path].Tags, want)
		}
	}
}
