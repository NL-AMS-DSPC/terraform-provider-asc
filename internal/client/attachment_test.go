package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// attachmentServer delays the volume projection after acknowledging attach/detach
// commands.
type attachmentServer struct {
	mu       sync.Mutex
	volume   Volume
	attaches int
	detaches int
	reads    int
	// settleAfter is the number of reads before the volume reports the end
	// state, which stands in for the asynchronous command completing.
	settleAfter int
	settled     func(*Volume)
}

// The volume reports the instance UUID; the VM read exposes it in its URN.
const (
	appVMID  = "20000000-0000-4000-8000-000000000001"
	appVMURN = "asc:atos:compute:tenant:virtual_machine:" + appVMID
)

// writeVM serves the machine read model the attachment code resolves the
// instance id from.
func writeVM(w http.ResponseWriter, name string) {
	writeEnvelope(w, vmData(VM{Name: name, URN: appVMURN, Status: "active"}))
}

func newAttachmentRuntime(t *testing.T, state *attachmentServer) *Runtime {
	t.Helper()
	return newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()

		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/attach/"):
			state.attaches++
			writeEnvelope(w, map[string]string{"volumeName": "data", "vmName": "app"})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/attach/"):
			state.detaches++
			writeEnvelope(w, map[string]string{"volumeName": "data", "vmName": "app"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/vms/"):
			writeVM(w, "app")
		case r.Method == http.MethodGet:
			state.reads++
			current := state.volume
			if state.reads > state.settleAfter && state.settled != nil {
				state.settled(&current)
			}
			writeEnvelope(w, current)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func writeEnvelope(w http.ResponseWriter, value any) {
	if volume, ok := value.(Volume); ok {
		value = volumeData(volume)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
}

func TestAttachVolumeWaitsForTheVolumeToReportAttached(t *testing.T) {
	t.Parallel()
	state := &attachmentServer{
		volume:      Volume{Name: "data", Status: "attaching"},
		settleAfter: 1,
		settled:     func(v *Volume) { v.Status = "attached"; v.AttachedToVM = appVMID },
	}
	runtime := newAttachmentRuntime(t, state)

	attached, err := runtime.AttachVolume(context.Background(), "data", "app")
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if attached.AttachedToVM != appVMID || attached.Status != "attached" {
		t.Fatalf("AttachVolume() = %#v, want attached to the app instance id", attached)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.attaches != 1 {
		t.Fatalf("attach calls = %d, want 1", state.attaches)
	}
	if state.reads < 2 {
		t.Fatalf("read calls = %d, want at least 2: the acknowledgement alone does not prove completion", state.reads)
	}
}

func TestAttachVolumeDoesNotAcceptAnAttachmentToAnotherMachine(t *testing.T) {
	t.Parallel()
	state := &attachmentServer{volume: Volume{Name: "data", Status: "attached", AttachedToVM: "other"}}
	runtime := newAttachmentRuntime(t, state)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := runtime.AttachVolume(ctx, "data", "app"); err == nil {
		t.Fatal("AttachVolume() error = nil, want a wait that never completes")
	}
}

func TestAttachVolumeSurfacesATerminalVolumeError(t *testing.T) {
	t.Parallel()
	state := &attachmentServer{volume: Volume{Name: "data", Status: "error", LastError: "no free slot"}}
	runtime := newAttachmentRuntime(t, state)

	_, err := runtime.AttachVolume(context.Background(), "data", "app")
	if err == nil || !strings.Contains(err.Error(), "no free slot") {
		t.Fatalf("AttachVolume() error = %v, want the gateway lastError", err)
	}
}

func TestDetachVolumeWaitsForTheAttachmentToClear(t *testing.T) {
	t.Parallel()
	state := &attachmentServer{
		volume:      Volume{Name: "data", Status: "detaching", AttachedToVM: appVMID},
		settleAfter: 1,
		settled:     func(v *Volume) { v.Status = "detached"; v.AttachedToVM = "" },
	}
	runtime := newAttachmentRuntime(t, state)

	if err := runtime.DetachVolume(context.Background(), "data", "app"); err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.detaches != 1 || state.reads < 2 {
		t.Fatalf("detach calls = %d, read calls = %d", state.detaches, state.reads)
	}
}

func TestDetachVolumeAcceptsAVolumeThatIsAlreadyGone(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			writeEnvelope(w, map[string]string{"volumeName": "data", "vmName": "app"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	if err := runtime.DetachVolume(context.Background(), "data", "app"); err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
}

func TestDetachVolumeAcceptsAn404FromTheDetachRoute(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if err := runtime.DetachVolume(context.Background(), "data", "app"); err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
}

// newVolumeReadRuntime serves the machine on /vms/ and the given volume
// everywhere else, which is the shape GetVolumeAttachment reads.
func newVolumeReadRuntime(t *testing.T, volume Volume) *Runtime {
	t.Helper()
	return newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/vms/") {
			writeVM(w, "app")
			return
		}
		writeEnvelope(w, volume)
	})
}

func TestGetVolumeAttachmentReportsAReassignedVolumeAsAbsent(t *testing.T) {
	t.Parallel()
	runtime := newVolumeReadRuntime(t, Volume{Name: "data", Status: "attached", AttachedToVM: "other"})

	_, err := runtime.GetVolumeAttachment(context.Background(), "data", "app")
	if !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetVolumeAttachment() error = %v, want ErrResourceNotFound", err)
	}
}

func TestGetVolumeAttachmentMatchesTheInstanceID(t *testing.T) {
	t.Parallel()
	runtime := newVolumeReadRuntime(t, Volume{Name: "data", Status: "attached", AttachedToVM: appVMID})

	current, err := runtime.GetVolumeAttachment(context.Background(), "data", "app")
	if err != nil {
		t.Fatalf("GetVolumeAttachment() error = %v, want the attachment matched on the instance id", err)
	}
	if current.AttachedToVM != appVMID {
		t.Fatalf("GetVolumeAttachment() = %#v", current)
	}
}

func TestGetVolumeAttachmentStillMatchesTheName(t *testing.T) {
	t.Parallel()
	runtime := newVolumeReadRuntime(t, Volume{Name: "data", Status: "attached", AttachedToVM: "app"})

	current, err := runtime.GetVolumeAttachment(context.Background(), "data", "app")
	if err != nil {
		t.Fatalf("GetVolumeAttachment() error = %v", err)
	}
	if current.AttachedToVM != "app" {
		t.Fatalf("GetVolumeAttachment() = %#v", current)
	}
}

func TestGetVolumeAttachmentReportsAMissingMachineAsAbsent(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/vms/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeEnvelope(w, Volume{Name: "data", Status: "attached", AttachedToVM: appVMID})
	})

	_, err := runtime.GetVolumeAttachment(context.Background(), "data", "app")
	if !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetVolumeAttachment() error = %v, want ErrResourceNotFound", err)
	}
}

func TestURNResourceIDRejectsAMalformedURN(t *testing.T) {
	t.Parallel()
	if got := urnResourceID(appVMURN); got != appVMID {
		t.Fatalf("urnResourceID(%q) = %q, want %q", appVMURN, got, appVMID)
	}
	for _, urn := range []string{"", "app", "asc:atos:compute:tenant:virtual_machine", "urn:atos:compute:t:virtual_machine:1"} {
		if got := urnResourceID(urn); got != "" {
			t.Fatalf("urnResourceID(%q) = %q, want an empty string", urn, got)
		}
	}
}

func TestAttachmentPathEscapesBothNames(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var paths []string
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		mu.Unlock()
		writeEnvelope(w, map[string]string{"volumeName": "data vol", "vmName": "app/vm"})
	})
	if err := runtime.DetachVolume(context.Background(), "data vol", "app/vm"); err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := "DELETE /api/infra-gateway/volumes/data%20vol/attach/app%2Fvm"; len(paths) == 0 || paths[0] != want {
		t.Fatalf("requests = %q, want the first to be %q", paths, want)
	}
}
