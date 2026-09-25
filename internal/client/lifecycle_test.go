package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newLifecycleRuntime(t *testing.T, api http.HandlerFunc) *Runtime {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		api(w, r)
	}))
	t.Cleanup(server.Close)
	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	runtime := NewRuntime(config)
	runtime.pollInterval = time.Millisecond
	runtime.lifecycleTimeout = time.Second
	return runtime
}

func TestCreateVPCRetriesTypedStaleReservation(t *testing.T) {
	t.Parallel()
	var posts atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if posts.Add(1) == 1 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			if posts.Load() == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": vpcData(VPC{Name: "test-vpc", Status: "active"})})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	if _, err := runtime.CreateVPC(context.Background(), CreateVPCRequest{Name: "test-vpc"}); err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if got := posts.Load(); got != 2 {
		t.Fatalf("POST count = %d, want 2", got)
	}
}

func TestCreateVMRetriesTypedStaleReservationAndIgnoresDeletingProjection(t *testing.T) {
	t.Parallel()
	var posts atomic.Int64
	var gets atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if posts.Add(1) == 1 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			switch gets.Add(1) {
			case 1:
				w.WriteHeader(http.StatusNotFound)
			case 2:
				_ = json.NewEncoder(w).Encode(map[string]any{"data": vmData(VM{Name: "test-vm", Status: "deleting"})})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"data": vmData(VM{Name: "test-vm", Status: "active"})})
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	created, err := runtime.CreateVM(context.Background(), CreateVMRequest{Name: "test-vm"})
	if err != nil {
		t.Fatalf("CreateVM() error = %v", err)
	}
	if created.Status != "active" {
		t.Fatalf("CreateVM() status = %q, want active", created.Status)
	}
	if got := posts.Load(); got != 2 {
		t.Fatalf("POST count = %d, want 2", got)
	}
}

func TestDeleteVMWaitsForAuthoritativeAbsence(t *testing.T) {
	t.Parallel()
	var gets atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if gets.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": vmData(VM{Name: "test-vm", Status: "deleting"})})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	if err := runtime.DeleteVM(context.Background(), "test-vm"); err != nil {
		t.Fatalf("DeleteVM() error = %v", err)
	}
	if got := gets.Load(); got != 2 {
		t.Fatalf("GET count = %d, want 2", got)
	}
}

func TestDeleteVPCWaitsForAuthoritativeAbsence(t *testing.T) {
	t.Parallel()
	var gets atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if gets.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": vpcData(VPC{Name: "test-vpc", Status: "deleting"})})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	if err := runtime.DeleteVPC(context.Background(), "test-vpc"); err != nil {
		t.Fatalf("DeleteVPC() error = %v", err)
	}
	if got := gets.Load(); got != 2 {
		t.Fatalf("GET count = %d, want 2", got)
	}
}

func TestDeleteVPCStopsOnTerminalError(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": vpcData(VPC{Name: "test-vpc", Status: "error", LastError: "delete failed"})})
	})

	err := runtime.DeleteVPC(context.Background(), "test-vpc")
	if err == nil || !strings.Contains(err.Error(), "delete failed") {
		t.Fatalf("DeleteVPC() error = %v, want terminal lastError", err)
	}
}

func TestDeleteVMHonorsContextDeadline(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": vmData(VM{Name: "test-vm", Status: "deleting"})})
	})
	runtime.pollInterval = time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := runtime.DeleteVM(ctx, "test-vm"); err == nil {
		t.Fatal("DeleteVM() error = nil, want context deadline")
	}
}
