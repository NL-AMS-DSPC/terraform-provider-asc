package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

const (
	ip1         = "10.0.0.1"
	ip2         = "10.0.0.2"
	reservedAt1 = "2026-09-23T14:00:00Z"
	reservedAt2 = "2026-09-23T15:00:00Z"
)

func newReservationTestRuntime(t *testing.T, handler http.HandlerFunc) *Runtime {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant", server.URL, "realm", "id", "secret", 30*time.Second)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	return NewRuntime(config)
}

// withToken serves the OAuth token route and delegates everything else, since
// IPAM responses are never wrapped in the infra-gateway "data" envelope.
func withToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		next(w, r)
	}
}

func TestReserveIPContract(t *testing.T) {
	t.Parallel()

	var path, authorization, tenant string
	runtime := newReservationTestRuntime(t, withToken(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		authorization = r.Header.Get("Authorization")
		tenant = r.Header.Get("X-Tenant")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Reservation{IP: ip1, ReservedAt: reservedAt1})
	}))

	reservation, err := runtime.ReserveIP(context.Background())
	if err != nil {
		t.Fatalf("ReserveIP: %v", err)
	}

	if path != "POST /api/ipam/reservations" {
		t.Errorf("request = %q, want POST /api/ipam/reservations", path)
	}
	if authorization != "Bearer token" {
		t.Errorf("Authorization = %q", authorization)
	}
	if tenant != "tenant" {
		t.Errorf("X-Tenant = %q", tenant)
	}
	if reservation.IP != ip1 || reservation.ReservedAt != reservedAt1 {
		t.Errorf("reservation = %+v", reservation)
	}
}

func TestReserveIPFailsOnErrorStatuses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ status int }{
		"unauthorized": {http.StatusUnauthorized},
		"forbidden":    {http.StatusForbidden},
		"server error": {http.StatusInternalServerError},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runtime := newReservationTestRuntime(t, withToken(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))

			if _, err := runtime.ReserveIP(context.Background()); err == nil {
				t.Fatalf("ReserveIP succeeded on HTTP %d", test.status)
			}
		})
	}
}

func TestListReservations(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		body string
		want []Reservation
	}{
		"multiple reservations": {
			body: `{"reservations":[{"ip":"10.0.0.1","reservedAt":"2026-09-23T14:00:00Z"},{"ip":"10.0.0.2","reservedAt":"2026-09-23T15:00:00Z"}]}`,
			want: []Reservation{
				{IP: ip1, ReservedAt: reservedAt1},
				{IP: ip2, ReservedAt: reservedAt2},
			},
		},
		"empty catalogue": {
			body: `{"reservations":[]}`,
			want: []Reservation{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runtime := newReservationTestRuntime(t, withToken(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))

			reservations, err := runtime.ListReservations(context.Background())
			if err != nil {
				t.Fatalf("ListReservations: %v", err)
			}
			if !reflect.DeepEqual(reservations, test.want) {
				t.Errorf("reservations = %+v, want %+v", reservations, test.want)
			}
		})
	}
}

func TestGetReservation(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		body    string
		want    Reservation
		wantErr error
	}{
		"match found": {
			body: `{"reservations":[{"ip":"10.0.0.1","reservedAt":"2026-09-23T14:00:00Z"}]}`,
			want: Reservation{IP: ip1, ReservedAt: reservedAt1},
		},
		"no match": {
			body:    `{"reservations":[]}`,
			wantErr: ErrResourceNotFound,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runtime := newReservationTestRuntime(t, withToken(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))

			reservation, err := runtime.GetReservation(context.Background(), ip1)
			if test.wantErr != nil {
				if err != test.wantErr {
					t.Fatalf("GetReservation() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetReservation: %v", err)
			}
			if reservation != test.want {
				t.Errorf("reservation = %+v, want %+v", reservation, test.want)
			}
		})
	}
}

func TestReleaseIPContract(t *testing.T) {
	t.Parallel()

	var path string
	var body releaseReservationRequest
	runtime := newReservationTestRuntime(t, withToken(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode release body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	if err := runtime.ReleaseIP(context.Background(), ip1); err != nil {
		t.Fatalf("ReleaseIP: %v", err)
	}
	if path != "DELETE /api/ipam/reservations" {
		t.Errorf("request = %q, want DELETE /api/ipam/reservations", path)
	}
	if body.IP != ip1 {
		t.Errorf("release body ip = %q, want 10.0.0.1", body.IP)
	}
}

func TestReleaseIPFailsOnErrorStatuses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ status int }{
		"not found":    {http.StatusNotFound},
		"unauthorized": {http.StatusUnauthorized},
		"server error": {http.StatusInternalServerError},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runtime := newReservationTestRuntime(t, withToken(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))

			if err := runtime.ReleaseIP(context.Background(), ip1); err == nil {
				t.Fatalf("ReleaseIP succeeded on HTTP %d", test.status)
			}
		})
	}
}
