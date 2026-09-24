package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

const reservationsPath = "/api/ipam/reservations"

// reservationStore hands out sequential addresses so tests can assert on a known IP.
type reservationStore struct {
	mu           sync.Mutex
	reservations []client.Reservation
	next         int
}

func (s *reservationStore) handlers(t *testing.T) map[string]http.HandlerFunc {
	t.Helper()
	return map[string]http.HandlerFunc{
		"POST " + reservationsPath: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.next++
			reservation := client.Reservation{
				IP:         fmt.Sprintf("10.0.0.%d", s.next),
				ReservedAt: "2026-09-23T14:00:00Z",
			}
			s.reservations = append(s.reservations, reservation)
			writeBare(w, http.StatusOK, reservation)
		},
		"GET " + reservationsPath: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			writeBare(w, http.StatusOK, map[string]any{"reservations": s.reservations})
		},
		"DELETE " + reservationsPath: func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				IP string `json:"ip"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode release body: %v", err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			kept := s.reservations[:0]
			for _, reservation := range s.reservations {
				if reservation.IP != body.IP {
					kept = append(kept, reservation)
				}
			}
			s.reservations = kept
			w.WriteHeader(http.StatusOK)
		},
	}
}

func TestIPReservationProvisioning(t *testing.T) {
	store := &reservationStore{}
	environment := newTestEnvironment(t, store.handlers(t))

	config := environment.providerConfig() + `resource "asc_ip" "test" {}`

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_ip.test", "ip", "10.0.0.1"),
				resource.TestCheckResourceAttr("asc_ip.test", "reserved_at", "2026-09-23T14:00:00Z"),
			),
		},
		{ResourceName: "asc_ip.test", ImportState: true, ImportStateId: "10.0.0.1", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "ip"},
	}})

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reservations) != 0 {
		t.Errorf("reservations left after destroy = %+v, want none", store.reservations)
	}
}

func TestIPReservationRefreshDropsAReservationReleasedOutsideTerraform(t *testing.T) {
	store := &reservationStore{}
	environment := newTestEnvironment(t, store.handlers(t))

	config := environment.providerConfig() + `resource "asc_ip" "test" {}`

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config, Check: resource.TestCheckResourceAttr("asc_ip.test", "ip", "10.0.0.1")},
		{
			Config: config,
			PreConfig: func() {
				store.mu.Lock()
				defer store.mu.Unlock()
				store.reservations = nil
			},
			// The provider must re-reserve when the address is gone
			Check: resource.TestCheckResourceAttr("asc_ip.test", "ip", "10.0.0.2"),
		},
	}})
}

func TestIPReservationsDataSourceListsReservedAddresses(t *testing.T) {
	store := &reservationStore{reservations: []client.Reservation{
		{IP: "10.0.0.1", ReservedAt: "2026-09-23T14:00:00Z"},
		{IP: "10.0.0.2", ReservedAt: "2026-09-23T15:00:00Z"},
	}}
	environment := newTestEnvironment(t, store.handlers(t))

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `data "asc_ips" "test" {}`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.asc_ips.test", "ips.#", "2"),
			resource.TestCheckResourceAttr("data.asc_ips.test", "ips.0.ip", "10.0.0.1"),
			resource.TestCheckResourceAttr("data.asc_ips.test", "ips.1.ip", "10.0.0.2"),
			resource.TestCheckResourceAttr("data.asc_ips.test", "ips.1.reserved_at", "2026-09-23T15:00:00Z"),
		),
	}}})
}
