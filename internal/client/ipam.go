package client

import (
	"context"
	"fmt"
	"net/http"
)

const reservationsPath = "/reservations"

// Reservation is a single IP address reserved through IPAM
type Reservation struct {
	IP         string `json:"ip"`
	ReservedAt string `json:"reservedAt"`
}

type reservationListResponse struct {
	Reservations []Reservation `json:"reservations"`
}

type releaseReservationRequest struct {
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

// ListReservations lists every IP address currently reserved
func (c *Runtime) ListReservations(ctx context.Context) ([]Reservation, error) {
	var list reservationListResponse
	if err := c.get(ctx, svcIPAM, reservationsPath, &list); err != nil {
		return nil, fmt.Errorf("list reservations: %w", err)
	}
	return list.Reservations, nil
}

// GetReservation finds one reserved IP address
func (c *Runtime) GetReservation(ctx context.Context, ip string) (Reservation, error) {
	reservations, err := c.ListReservations(ctx)
	if err != nil {
		return Reservation{}, fmt.Errorf("read reservations %q: %w", ip, err)
	}
	for _, reservation := range reservations {
		if reservation.IP == ip {
			return reservation, nil
		}
	}
	return Reservation{}, ErrResourceNotFound
}

// ReleaseIP releases a reserved IP address
func (c *Runtime) ReleaseIP(ctx context.Context, ip string) error {
	if err := c.request(ctx, svcIPAM, http.MethodDelete, reservationsPath, releaseReservationRequest{IP: ip}, nil); err != nil {
		return fmt.Errorf("release ip %q: %w", ip, err)
	}
	return nil
}
