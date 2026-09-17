package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func vmGroupRequest() CreateVMGroupRequest {
	return CreateVMGroupRequest{
		Name: "test-group", SKUID: "gp-2", VPCID: "00000000-0000-4000-8000-000000000001",
		Image: "ubuntu-24.04", Tags: []Tag{{Key: "env", Value: "test"}},
		AutoscalingPolicy: AutoscalingPolicy{
			MinReplicas: 2, MaxReplicas: 6,
			Cron: &CronRule{Timezone: "Europe/Amsterdam", Start: "0 8 * * 1-5", End: "0 18 * * 1-5", DesiredReplicas: 4},
		},
	}
}

func TestCreateVMGroupSendsTheGatewayRequestShape(t *testing.T) {
	t.Parallel()
	var body map[string]any
	var path atomic.Value
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			path.Store(r.URL.Path)
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": VMGroup{Name: "test-group", Status: "active"}})
	})

	if _, err := runtime.CreateVMGroup(context.Background(), vmGroupRequest()); err != nil {
		t.Fatalf("CreateVMGroup() error = %v", err)
	}
	if got := path.Load(); got != "/api/infra-gateway/vm-groups/" {
		t.Fatalf("create path = %v", got)
	}
	for key, want := range map[string]any{"name": "test-group", "skuID": "gp-2", "vpcID": "00000000-0000-4000-8000-000000000001", "image": "ubuntu-24.04"} {
		if body[key] != want {
			t.Errorf("body[%q] = %v, want %v", key, body[key], want)
		}
	}
	// The gateway accepts the policy under "autoscalers" on create and reports
	// it under "autoscalingPolicy" on read; getting the request key wrong is
	// silently accepted as an empty policy and fails validation instead.
	autoscalers, ok := body["autoscalers"].(map[string]any)
	if !ok {
		t.Fatalf("body[autoscalers] = %#v, want an object", body["autoscalers"])
	}
	if autoscalers["min_replicas"] != float64(2) || autoscalers["max_replicas"] != float64(6) {
		t.Errorf("autoscalers replicas = %#v", autoscalers)
	}
	cron, ok := autoscalers["cron"].(map[string]any)
	if !ok {
		t.Fatalf("autoscalers[cron] = %#v, want an object", autoscalers["cron"])
	}
	for key, want := range map[string]any{"timezone": "Europe/Amsterdam", "start": "0 8 * * 1-5", "end": "0 18 * * 1-5", "desiredReplicas": float64(4)} {
		if cron[key] != want {
			t.Errorf("cron[%q] = %v, want %v", key, cron[key], want)
		}
	}
}

func TestCreateVMGroupOmitsAnAbsentCronRule(t *testing.T) {
	t.Parallel()
	var body map[string]any
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": VMGroup{Name: "test-group", Status: "active"}})
	})

	request := vmGroupRequest()
	request.AutoscalingPolicy.Cron = nil
	if _, err := runtime.CreateVMGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateVMGroup() error = %v", err)
	}
	// An empty cron object would fail validation: the gateway requires all four
	// of its fields as soon as the object is present.
	if _, exists := body["autoscalers"].(map[string]any)["cron"]; exists {
		t.Fatalf("autoscalers = %#v, want no cron key", body["autoscalers"])
	}
}

func TestCreateVMGroupWaitsForTheGroupToAppear(t *testing.T) {
	t.Parallel()
	var gets atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			return
		}
		switch gets.Add(1) {
		case 1:
			w.WriteHeader(http.StatusNotFound)
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VMGroup{Name: "test-group", Status: "pending"}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VMGroup{Name: "test-group", Status: "active"}})
		}
	})

	created, err := runtime.CreateVMGroup(context.Background(), vmGroupRequest())
	if err != nil {
		t.Fatalf("CreateVMGroup() error = %v", err)
	}
	// Pending is a settled read, not an absent one: the create returns as soon
	// as the projection exists and is not a leftover being deleted.
	if created.Status != "pending" {
		t.Fatalf("CreateVMGroup() status = %q, want pending", created.Status)
	}
	if got := gets.Load(); got != 2 {
		t.Fatalf("GET count = %d, want 2", got)
	}
}

// TestCreateVMGroupReportsAConflictWithoutRetrying is the regression.
// infra-gateway holds a group's name in vm_names_in_use until it acknowledges
// the group's deletion, so a 409 whose group is absent from the read model is
// not a reservation about to be released. Retrying it polled until the
// lifecycle timeout and then reported a deadline instead of the reason, on
// every asc_vm_group replacement.
func TestCreateVMGroupReportsAConflictWithoutRetrying(t *testing.T) {
	t.Parallel()
	var posts atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			posts.Add(1)
			w.WriteHeader(http.StatusConflict)
		case http.MethodGet:
			// The group is gone from the read model, which is exactly the
			// shape the old retry mistook for a stale reservation.
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	_, err := runtime.CreateVMGroup(context.Background(), vmGroupRequest())
	if err == nil || !errors.Is(err, ErrResourceConflict) {
		t.Fatalf("CreateVMGroup() error = %v, want a conflict", err)
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("CreateVMGroup() error = %v, want it to name the cause", err)
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("POST count = %d, want 1: the conflict must not be retried", got)
	}
}

func TestCreateVMGroupReportsAGenuineConflict(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": VMGroup{Name: "test-group", Status: "active"}})
	})

	_, err := runtime.CreateVMGroup(context.Background(), vmGroupRequest())
	if err == nil || !errors.Is(err, ErrResourceConflict) {
		t.Fatalf("CreateVMGroup() error = %v, want a conflict", err)
	}
}

func TestCreateVMGroupSurfacesTerminalError(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": VMGroup{Name: "test-group", Status: "error", LastError: "no capacity for the requested SKU"}})
	})

	_, err := runtime.CreateVMGroup(context.Background(), vmGroupRequest())
	if err == nil || !strings.Contains(err.Error(), "no capacity for the requested SKU") {
		t.Fatalf("CreateVMGroup() error = %v, want the gateway lastError", err)
	}
}

func TestGetVMGroupMapsAbsenceAndSortsAttachedVolumes(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"name": "test-group", "status": "active",
			"attachedVolumes": []string{"logs", "data", "scratch"},
			"autoscalingPolicy": map[string]any{
				"min_replicas": 2, "max_replicas": 6,
				"cron": map[string]any{"timezone": "UTC", "start": "0 8 * * 1-5", "end": "0 18 * * 1-5", "desiredReplicas": 4},
			},
		}})
	})

	if _, err := runtime.GetVMGroup(context.Background(), "missing"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetVMGroup(missing) error = %v, want ErrResourceNotFound", err)
	}

	group, err := runtime.GetVMGroup(context.Background(), "test-group")
	if err != nil {
		t.Fatalf("GetVMGroup() error = %v", err)
	}
	// The gateway builds attachedVolumes by ranging over a map, so the client
	// sorts it to keep two reads of the same group identical.
	if got := strings.Join(group.AttachedVolumes, ","); got != "data,logs,scratch" {
		t.Fatalf("attached volumes = %q, want them sorted", got)
	}
	if group.AutoscalingPolicy.MinReplicas != 2 || group.AutoscalingPolicy.MaxReplicas != 6 {
		t.Fatalf("autoscaling policy = %#v", group.AutoscalingPolicy)
	}
	if group.AutoscalingPolicy.Cron == nil || group.AutoscalingPolicy.Cron.DesiredReplicas != 4 {
		t.Fatalf("cron rule = %#v", group.AutoscalingPolicy.Cron)
	}
}

func TestDeleteVMGroupWaitsForAuthoritativeAbsence(t *testing.T) {
	t.Parallel()
	var gets atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if gets.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": VMGroup{Name: "test-group", Status: "deleting"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	if err := runtime.DeleteVMGroup(context.Background(), "test-group"); err != nil {
		t.Fatalf("DeleteVMGroup() error = %v", err)
	}
	if got := gets.Load(); got != 2 {
		t.Fatalf("GET count = %d, want 2", got)
	}
}

func TestDeleteVMGroupTreatsAbsenceAsSuccess(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if err := runtime.DeleteVMGroup(context.Background(), "test-group"); err != nil {
		t.Fatalf("DeleteVMGroup() error = %v, want the absent group to count as deleted", err)
	}
}
