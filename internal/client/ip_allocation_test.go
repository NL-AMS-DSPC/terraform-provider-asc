package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// ipAllocationServer delays the VM projection after acknowledging allocate and deallocate commands
type ipAllocationServer struct {
	mu            sync.Mutex
	vm            VM
	allocations   []string
	deallocations []string
	reads         int
	// settleAfter is the number of reads before the VM reports the end state,
	// which stands in for the asynchronous command completing.
	settleAfter int
	settled     func(*VM)
}

func newIPAllocationRuntime(t *testing.T, state *ipAllocationServer) *Runtime {
	t.Helper()
	return newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()

		switch {
		case r.URL.Path == "/api/infra-gateway/vms/app/ip" && (r.Method == http.MethodPost || r.Method == http.MethodDelete):
			var body IPAllocationRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode %s body: %v", r.Method, err)
			}
			if r.Method == http.MethodPost {
				state.allocations = append(state.allocations, body.IP)
			} else {
				state.deallocations = append(state.deallocations, body.IP)
			}
			writeEnvelope(w, map[string]string{"vmName": "app", "ip": body.IP})
		case r.URL.Path == "/api/infra-gateway/vms/app" && r.Method == http.MethodGet:
			state.reads++
			current := state.vm
			current.Addresses.PublicIPs = slices.Clone(state.vm.Addresses.PublicIPs)
			if state.reads > state.settleAfter && state.settled != nil {
				state.settled(&current)
			}
			writeEnvelope(w, current)
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func TestAllocateIPWaitsForTheVMToListTheAddress(t *testing.T) {
	t.Parallel()
	state := &ipAllocationServer{
		vm:          VM{Name: "app", Status: "active"},
		settleAfter: 1,
		settled:     func(v *VM) { v.Addresses.PublicIPs = []string{"10.0.0.1"} },
	}
	runtime := newIPAllocationRuntime(t, state)

	allocated, err := runtime.AllocateIP(context.Background(), "app", "10.0.0.1")
	if err != nil {
		t.Fatalf("AllocateIP() error = %v", err)
	}
	if !slices.Equal(allocated.Addresses.PublicIPs, []string{"10.0.0.1"}) {
		t.Fatalf("AllocateIP() = %#v, want the address listed", allocated.Addresses)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if !slices.Equal(state.allocations, []string{"10.0.0.1"}) {
		t.Fatalf("allocations = %v, want one request for 10.0.0.1", state.allocations)
	}
	if state.reads < 2 {
		t.Fatalf("read calls = %d, want at least 2: the acknowledgement alone does not prove completion", state.reads)
	}
}

func TestAllocateIPFailures(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		vm      VM
		wantErr string
	}{
		"another address listed": {
			vm:      VM{Name: "app", Status: "active", Addresses: Addresses{PublicIPs: []string{"10.0.0.2"}}},
			wantErr: context.DeadlineExceeded.Error(),
		},
		"terminal VM error": {
			vm:      VM{Name: "app", Status: "error", LastError: "address already in use"},
			wantErr: "address already in use",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtime := newIPAllocationRuntime(t, &ipAllocationServer{vm: test.vm})

			// The deadline ends a wait that never completes.
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err := runtime.AllocateIP(ctx, "app", "10.0.0.1")
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("AllocateIP() error = %v, want an error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestDeallocateIPWaitsForTheAddressToDisappear(t *testing.T) {
	t.Parallel()
	state := &ipAllocationServer{
		vm:          VM{Name: "app", Status: "active", Addresses: Addresses{PublicIPs: []string{"10.0.0.1", "10.0.0.2"}}},
		settleAfter: 1,
		settled:     func(v *VM) { v.Addresses.PublicIPs = []string{"10.0.0.2"} },
	}
	runtime := newIPAllocationRuntime(t, state)

	if err := runtime.DeallocateIP(context.Background(), "app", "10.0.0.1"); err != nil {
		t.Fatalf("DeallocateIP() error = %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !slices.Equal(state.deallocations, []string{"10.0.0.1"}) || state.reads < 2 {
		t.Fatalf("deallocations = %v, read calls = %d", state.deallocations, state.reads)
	}
}

func TestDeallocateIPTreatsA404AsDone(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deleteStatus int
	}{
		"VM already gone":              {deleteStatus: http.StatusOK},
		"deallocate route returns 404": {deleteStatus: http.StatusNotFound},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && test.deleteStatus == http.StatusOK {
					writeEnvelope(w, map[string]string{"vmName": "app", "ip": "10.0.0.1"})
					return
				}
				w.WriteHeader(http.StatusNotFound)
			})

			if err := runtime.DeallocateIP(context.Background(), "app", "10.0.0.1"); err != nil {
				t.Fatalf("DeallocateIP() error = %v", err)
			}
		})
	}
}

func TestGetIPAllocation(t *testing.T) {
	t.Parallel()

	listed := VM{Name: "app", Status: "active", Addresses: Addresses{PublicIPs: []string{"10.0.0.2"}}}
	tests := map[string]struct {
		status       int
		vm           VM
		ip           string
		wantNotFound bool
		// wantErr expects an error that must not be mistaken for absence.
		wantErr bool
	}{
		"listed address": {
			status: http.StatusOK,
			vm:     listed,
			ip:     "10.0.0.2",
		},
		"unlisted address": {
			status:       http.StatusOK,
			vm:           listed,
			ip:           "10.0.0.1",
			wantNotFound: true,
		},
		"missing VM": {
			status:       http.StatusNotFound,
			ip:           "10.0.0.1",
			wantNotFound: true,
		},
		"gateway failure": {
			status:  http.StatusInternalServerError,
			ip:      "10.0.0.1",
			wantErr: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, _ *http.Request) {
				if test.status != http.StatusOK {
					w.WriteHeader(test.status)
					return
				}
				writeEnvelope(w, test.vm)
			})

			_, err := runtime.GetIPAllocation(context.Background(), "app", test.ip)
			switch {
			case test.wantNotFound:
				if !errors.Is(err, ErrResourceNotFound) {
					t.Fatalf("GetIPAllocation() error = %v, want ErrResourceNotFound", err)
				}
			case test.wantErr:
				if err == nil || errors.Is(err, ErrResourceNotFound) {
					t.Fatalf("GetIPAllocation() error = %v, want an error other than ErrResourceNotFound", err)
				}
			case err != nil:
				t.Fatalf("GetIPAllocation() error = %v", err)
			}
		})
	}
}

func TestGetVMSortsPublicIPs(t *testing.T) {
	t.Parallel()
	state := &ipAllocationServer{vm: VM{Name: "app", Status: "active", Addresses: Addresses{PublicIPs: []string{"10.0.0.10", "10.0.0.2"}}}}
	runtime := newIPAllocationRuntime(t, state)

	current, err := runtime.GetVM(context.Background(), "app")
	if err != nil {
		t.Fatalf("GetVM() error = %v", err)
	}
	if !slices.Equal(current.Addresses.PublicIPs, []string{"10.0.0.2", "10.0.0.10"}) {
		t.Fatalf("public IPs = %v, want sorted by address", current.Addresses.PublicIPs)
	}
}

func TestVMIPPathEscapesTheName(t *testing.T) {
	t.Parallel()
	if got, want := vmIPPath("app/vm"), "/vms/app%2Fvm/ip"; got != want {
		t.Fatalf("vmIPPath() = %q, want %q", got, want)
	}
}
