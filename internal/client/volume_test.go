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

func TestCreateVolumeWaitsUntilProvisioningSettles(t *testing.T) {
	t.Parallel()
	var posts, gets atomic.Int64
	var body CreateVolumeRequest
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/infra-gateway/volumes/":
			posts.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			writeVolumeData(w, http.StatusCreated, map[string]string{"name": "data"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/infra-gateway/volumes/data":
			if gets.Add(1) == 1 {
				writeVolumeData(w, http.StatusOK, Volume{Name: "data", Size: "10Gi", Status: "creating"})
				return
			}
			writeVolumeData(w, http.StatusOK, Volume{Name: "data", Size: "10Gi", Status: "detached"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	created, err := runtime.CreateVolume(context.Background(), CreateVolumeRequest{
		Name: "data", Size: "10Gi", StorageClass: "standard", AccessMode: "ReadWriteOnce",
		Tags: []Tag{{Key: "env", Value: "test"}},
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if created.Status != "detached" {
		t.Fatalf("CreateVolume() status = %q, want detached", created.Status)
	}
	if gets.Load() != 2 {
		t.Fatalf("GET count = %d, want 2 (creating, then settled)", gets.Load())
	}
	want := CreateVolumeRequest{Name: "data", Size: "10Gi", StorageClass: "standard", AccessMode: "ReadWriteOnce", Tags: []Tag{{Key: "env", Value: "test"}}}
	if body.Name != want.Name || body.Size != want.Size || body.StorageClass != want.StorageClass || body.AccessMode != want.AccessMode || len(body.Tags) != 1 || body.Tags[0] != want.Tags[0] {
		t.Fatalf("create body = %#v, want %#v", body, want)
	}
}

func TestCreateVolumeReportsUntypedConflictWithoutRetrying(t *testing.T) {
	t.Parallel()
	var posts atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		posts.Add(1)
		// infra-gateway maps a duplicate volume name to an untyped 500, and a
		// 500 may mean the create partially succeeded.
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := runtime.CreateVolume(context.Background(), CreateVolumeRequest{Name: "data", Size: "10Gi", StorageClass: "standard", AccessMode: "ReadWriteOnce"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("CreateVolume() error = %v, want the verbatim HTTP 500", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("POST count = %d, want 1: an untyped 500 must not be retried", posts.Load())
	}
}

func TestGetVolumeReportsAbsenceAsNotFound(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if _, err := runtime.GetVolume(context.Background(), "missing"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetVolume() error = %v, want ErrResourceNotFound", err)
	}
}

func TestResizeVolumePostsSizeAndWaitsForTheNewByteCount(t *testing.T) {
	t.Parallel()
	var body resizeVolumeRequest
	var gets atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/infra-gateway/volumes/data/resize":
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode resize body: %v", err)
			}
			writeVolumeData(w, http.StatusOK, map[string]string{"name": "data", "size": "20Gi"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/infra-gateway/volumes/data":
			if gets.Add(1) == 1 {
				writeVolumeData(w, http.StatusOK, Volume{Name: "data", Size: "10Gi", Status: "resizing"})
				return
			}
			// The gateway is free to echo an equal size spelled differently.
			writeVolumeData(w, http.StatusOK, Volume{Name: "data", Size: "20480Mi", Status: "detached"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	resized, err := runtime.ResizeVolume(context.Background(), "data", "20Gi")
	if err != nil {
		t.Fatalf("ResizeVolume() error = %v", err)
	}
	if body.Size != "20Gi" {
		t.Fatalf("resize body size = %q, want 20Gi", body.Size)
	}
	if resized.Size != "20480Mi" || gets.Load() != 2 {
		t.Fatalf("ResizeVolume() = %#v after %d reads, want the settled read", resized, gets.Load())
	}
}

func TestUpdateVolumeTagsSendsTheComputedPatchAndReadsBack(t *testing.T) {
	t.Parallel()
	var patch tagPatchRequest
	desired := []Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}}
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			if r.URL.Path != "/api/infra-gateway/volumes/data" {
				t.Errorf("patch path = %q", r.URL.Path)
			}
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Errorf("decode patch body: %v", err)
			}
			writeVolumeData(w, http.StatusOK, Volume{Name: "data", Size: "10Gi", Status: "detached"})
		case http.MethodGet:
			writeVolumeData(w, http.StatusOK, Volume{Name: "data", Size: "10Gi", Status: "detached", Tags: desired})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	current := []Tag{{Key: "env", Value: "staging"}, {Key: "stale", Value: "yes"}}
	updated, err := runtime.UpdateVolumeTags(context.Background(), "data", current, desired)
	if err != nil {
		t.Fatalf("UpdateVolumeTags() error = %v", err)
	}
	// The gateway patches rather than replaces, so the removed key travels as an
	// explicit empty value and the unchanged keys are left out.
	want := []TagPatch{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}, {Key: "stale"}}
	if len(patch.Tags) != len(want) {
		t.Fatalf("patch = %#v, want %#v", patch.Tags, want)
	}
	for i, entry := range want {
		if patch.Tags[i] != entry {
			t.Fatalf("patch = %#v, want %#v", patch.Tags, want)
		}
	}
	if !sameTags(updated.Tags, desired) {
		t.Fatalf("UpdateVolumeTags() tags = %#v, want %#v", updated.Tags, desired)
	}
}

func TestDeleteVolumeWaitsForAuthoritativeAbsence(t *testing.T) {
	t.Parallel()
	var gets atomic.Int64
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			writeVolumeData(w, http.StatusOK, map[string]string{"deletedId": "data"})
			return
		}
		if gets.Add(1) == 1 {
			writeVolumeData(w, http.StatusOK, Volume{Name: "data", Size: "10Gi", Status: "deleting"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	if err := runtime.DeleteVolume(context.Background(), "data"); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if gets.Load() != 2 {
		t.Fatalf("GET count = %d, want 2 (deleting, then gone)", gets.Load())
	}
}

func TestDeleteVolumeReportsAttachedConflict(t *testing.T) {
	t.Parallel()
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		// infra-gateway refuses to delete a volume that is still attached.
		w.WriteHeader(http.StatusConflict)
	})

	if err := runtime.DeleteVolume(context.Background(), "data"); !errors.Is(err, ErrResourceConflict) {
		t.Fatalf("DeleteVolume() error = %v, want ErrResourceConflict", err)
	}
}

func TestParseQuantity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value   string
		want    int64
		wantErr bool
	}{
		{value: "1024", want: 1024},
		{value: "10Ki", want: 10240},
		{value: "10Mi", want: 10 << 20},
		{value: "10Gi", want: 10 << 30},
		{value: "10240Mi", want: 10 << 30},
		{value: "10485760Ki", want: 10 << 30},
		{value: "1Ti", want: 1 << 40},
		{value: "1Pi", want: 1 << 50},
		{value: "1Ei", want: 1 << 60},
		{value: "500k", want: 500_000},
		{value: "500M", want: 500_000_000},
		{value: "1G", want: 1_000_000_000},
		{value: "1T", want: 1_000_000_000_000},
		{value: "1P", want: 1_000_000_000_000_000},
		{value: "1E", want: 1_000_000_000_000_000_000},
		{value: " 10Gi ", want: 10 << 30},
		{value: "", wantErr: true},
		{value: "10GB", wantErr: true},
		{value: "1.5Gi", wantErr: true},
		{value: "-1Gi", wantErr: true},
		{value: "ten", wantErr: true},
		{value: "10Ei0", wantErr: true},
		{value: "9223372036854775807Ei", wantErr: true},
	}
	for _, test := range tests {
		got, err := ParseQuantity(test.value)
		if test.wantErr {
			if err == nil {
				t.Errorf("ParseQuantity(%q) = %d, want an error", test.value, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseQuantity(%q) error = %v", test.value, err)
			continue
		}
		if got != test.want {
			t.Errorf("ParseQuantity(%q) = %d, want %d", test.value, got, test.want)
		}
	}

	// Equal sizes spelled differently must be indistinguishable: that is what
	// lets the resource keep the configured spelling in state.
	for _, pair := range [][2]string{{"10Gi", "10240Mi"}, {"1T", "1000G"}, {"1Ki", "1024"}} {
		left, leftErr := ParseQuantity(pair[0])
		right, rightErr := ParseQuantity(pair[1])
		if leftErr != nil || rightErr != nil || left != right {
			t.Errorf("ParseQuantity(%q) = %d, ParseQuantity(%q) = %d, want equal", pair[0], left, pair[1], right)
		}
	}
}

func writeVolumeData(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
}
