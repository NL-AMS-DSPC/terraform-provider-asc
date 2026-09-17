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

// attachmentServer serves the volume read model and records the attach and
// detach calls. The attach and detach routes answer with an acknowledgement
// only, exactly as infra-gateway does, so the tests prove the client waits on
// the volume rather than on the 200.
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
	_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
}

func TestAttachVolumeWaitsForTheVolumeToReportAttached(t *testing.T) {
	t.Parallel()
	state := &attachmentServer{
		volume:      Volume{Name: "data", Status: "attaching"},
		settleAfter: 1,
		settled:     func(v *Volume) { v.Status = "attached"; v.AttachedToVM = "app" },
	}
	runtime := newAttachmentRuntime(t, state)

	attached, err := runtime.AttachVolume(context.Background(), "data", "app")
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if attached.AttachedToVM != "app" || attached.Status != "attached" {
		t.Fatalf("AttachVolume() = %#v, want attached to app", attached)
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

// TestAttachVolumeDoesNotAcceptAnAttachmentToAnotherMachine guards the case the
// status alone would wave through: the volume is attached, but not here.
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
		volume:      Volume{Name: "data", Status: "detaching", AttachedToVM: "app"},
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

// TestDetachVolumeAcceptsAVolumeThatIsAlreadyGone covers the destroy ordering
// Terraform can produce: the volume was removed first, so there is nothing left
// to detach and the wait must not sit until the deadline.
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

func TestGetVolumeAttachmentReportsAReassignedVolumeAsAbsent(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, Volume{Name: "data", Status: "attached", AttachedToVM: "other"})
	})

	_, err := runtime.GetVolumeAttachment(context.Background(), "data", "app")
	if !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetVolumeAttachment() error = %v, want ErrResourceNotFound", err)
	}
}

func TestGetVolumeAttachmentReturnsTheAttachedVolume(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, Volume{Name: "data", Status: "attached", AttachedToVM: "app"})
	})

	current, err := runtime.GetVolumeAttachment(context.Background(), "data", "app")
	if err != nil {
		t.Fatalf("GetVolumeAttachment() error = %v", err)
	}
	if current.AttachedToVM != "app" {
		t.Fatalf("GetVolumeAttachment() = %#v", current)
	}
}

func TestAttachmentPathEscapesBothNames(t *testing.T) {
	t.Parallel()
	if got, want := attachmentPath("data vol", "app/vm"), "/volumes/data%20vol/attach/app%2Fvm"; got != want {
		t.Fatalf("attachmentPath() = %q, want %q", got, want)
	}
}
