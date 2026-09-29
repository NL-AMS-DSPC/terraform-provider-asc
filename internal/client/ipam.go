package client

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

const reservationsPath = "/reservations"

// Reservation is a single IP address reserved through IPAM.
// AllocatedTo is the URN of the Workload (e.g. VM) the address is allocated to, and nil while it is free.
type Reservation struct {
	IP          string  `json:"ip"`
	ReservedAt  string  `json:"reservedAt"`
	AllocatedTo *string `json:"allocatedTo"`
}

type reservationListResponse struct {
	Reservations []Reservation `json:"reservations"`
}

type reservationRequest struct {
	IP string `json:"ip"`
}

// ReserveIP asks IPAM to reserve a random IP address
func (c *Runtime) ReserveIP(ctx context.Context) (Reservation, error) {
	var reservation Reservation
	if err := c.request(ctx, svcIPAM, http.MethodPost, reservationsPath, nil, &reservation); err != nil {
		return Reservation{}, fmt.Errorf("reserve ip: %w", err)
	}
	return reservation, nil
}

// ListReservations lists every IP address currently reserved, sorted by address so the
// order IPAM returns them in cannot change which entry sits at a given index
func (c *Runtime) ListReservations(ctx context.Context) ([]Reservation, error) {
	var list reservationListResponse
	if err := c.get(ctx, svcIPAM, reservationsPath, &list); err != nil {
		return nil, fmt.Errorf("list reservations: %w", err)
	}
	slices.SortFunc(list.Reservations, func(a, b Reservation) int { return CompareAddresses(a.IP, b.IP) })
	return list.Reservations, nil
}

// GetReservation finds one reserved IP address
func (c *Runtime) GetReservation(ctx context.Context, ip string) (Reservation, error) {
	var reservation Reservation
	if err := c.get(ctx, svcIPAM, reservationsPath+"/"+escapedPathSegment(ip), &reservation); err != nil {
		return Reservation{}, fmt.Errorf("get reservation: %w", err)
	}
	return reservation, nil
}

// CompareAddresses orders IP addresses numerically, so 10.0.0.2 comes before 10.0.0.10
func CompareAddresses(a, b string) int {
	addressA, errA := netip.ParseAddr(a)
	addressB, errB := netip.ParseAddr(b)
	switch {
	case errA == nil && errB == nil:
		return addressA.Compare(addressB)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// ReleaseIP releases a reserved IP address
func (c *Runtime) ReleaseIP(ctx context.Context, ip string) error {
	if err := c.request(ctx, svcIPAM, http.MethodDelete, reservationsPath, reservationRequest{IP: ip}, nil); err != nil {
		return fmt.Errorf("release ip %q: %w", ip, err)
	}
	return nil
}
