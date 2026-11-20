package client

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const testVNICURN = "asc:atos:network:tenant:vnic:e6981388-97c7-4279-ab96-b270dd70b7df"

func TestVNICIDFromURN(t *testing.T) {
	t.Parallel()
	for urn, want := range map[string]string{
		testVNICURN: "e6981388-97c7-4279-ab96-b270dd70b7df",
		"plain":     "plain",
		"":          "",
	} {
		if got := VNICIDFromURN(urn); got != want {
			t.Errorf("VNICIDFromURN(%q) = %q, want %q", urn, got, want)
		}
	}
}

func TestCreateVNICWaitsUntilItLeavesPending(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	reads := 0
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "POST /api/infra-gateway/vnics":
			w.WriteHeader(http.StatusCreated)
			writeEnvelope(w, vnicData(VNIC{URN: testVNICURN, Status: "pending"}))
		case "GET /api/infra-gateway/vnics/e6981388-97c7-4279-ab96-b270dd70b7df":
			reads++
			vnic := VNIC{URN: testVNICURN, Status: "pending"}
			if reads > 2 {
				vnic.Status, vnic.PrivateIP = "detached", "10.0.0.5"
			}
			writeEnvelope(w, vnicData(vnic))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	vnic, err := runtime.CreateVNIC(context.Background(), CreateVNICRequest{SubnetURN: "urn:subnet"})
	if err != nil {
		t.Fatalf("CreateVNIC() error = %v", err)
	}
	if vnic.ID != "e6981388-97c7-4279-ab96-b270dd70b7df" || vnic.Status != "detached" || vnic.PrivateIP != "10.0.0.5" {
		t.Errorf("CreateVNIC() = %#v", vnic)
	}
}

func TestCreateVNICFailsOnErrorStatus(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		writeEnvelope(w, vnicData(VNIC{URN: testVNICURN, Status: "error", LastError: "no free IP in the subnet"}))
	})

	_, err := runtime.CreateVNIC(context.Background(), CreateVNICRequest{SubnetURN: "urn:subnet"})
	if err == nil || !strings.Contains(err.Error(), "no free IP in the subnet") {
		t.Fatalf("CreateVNIC() error = %v, want the error reason", err)
	}
}

// A VNIC of a machine destroyed in the same apply is released asynchronously, so deletion
// retries the API's conflict.
func TestDeleteVNICRetriesWhileAttached(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	deletes := 0
	deleted := false
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodDelete:
			deletes++
			if deletes < 3 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			writeEnvelope(w, map[string]string{"deleted": "id"})
		case http.MethodGet:
			if deleted {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeEnvelope(w, vnicData(VNIC{URN: testVNICURN, Status: "detaching"}))
		}
	})

	if err := runtime.DeleteVNIC(context.Background(), "id"); err != nil {
		t.Fatalf("DeleteVNIC() error = %v", err)
	}
	if deletes != 3 {
		t.Errorf("deletes = %d, want 3", deletes)
	}
}

// A machine's VNIC order is final once every attach has finished, so a create returns
// only then, with the order the API reports at that point.
func TestCreateVMWaitsForItsVNICsToAttach(t *testing.T) {
	t.Parallel()
	var vnicReads atomic.Int32
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/vms/app"):
			vnics := []string{"urn:vnic:b"}
			if vnicReads.Load() >= 2 {
				vnics = []string{"urn:vnic:a", "urn:vnic:b"}
			}
			writeEnvelope(w, vmData(VM{URN: "urn:vm", Name: "app", Status: "active", VNICs: vnics}))
		case strings.HasSuffix(r.URL.Path, "/vnics/a"):
			status := "attaching"
			if vnicReads.Add(1) >= 2 {
				status = "attached"
			}
			writeEnvelope(w, vnicData(VNIC{URN: "urn:vnic:a", VMURN: "urn:vm", Status: status}))
		case strings.HasSuffix(r.URL.Path, "/vnics/b"):
			writeEnvelope(w, vnicData(VNIC{URN: "urn:vnic:b", VMURN: "urn:vm", Status: "attached"}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	vm, err := runtime.CreateVM(context.Background(), CreateVMRequest{Name: "app", VNICs: []string{"urn:vnic:a", "urn:vnic:b"}})
	if err != nil {
		t.Fatalf("CreateVM() error = %v", err)
	}
	if vnicReads.Load() < 2 {
		t.Errorf("VNIC a read %d times, want a read after it attached", vnicReads.Load())
	}
	if strings.Join(vm.VNICs, ",") != "urn:vnic:a,urn:vnic:b" {
		t.Errorf("VNICs = %v, want the order read after every attach", vm.VNICs)
	}
}

func TestCreateVMFailsWhenAVNICFailsToAttach(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/vms/app"):
			writeEnvelope(w, vmData(VM{URN: "urn:vm", Name: "app", Status: "active"}))
		default:
			writeEnvelope(w, vnicData(VNIC{URN: "urn:vnic:a", VMURN: "urn:vm", Status: "error", LastError: "no free slot"}))
		}
	})

	_, err := runtime.CreateVM(context.Background(), CreateVMRequest{Name: "app", VNICs: []string{"urn:vnic:a"}})
	if err == nil || !strings.Contains(err.Error(), "no free slot") {
		t.Fatalf("CreateVM() error = %v, want the VNIC's attach error", err)
	}
}
