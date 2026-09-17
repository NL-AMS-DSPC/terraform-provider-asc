package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// newWaiterRuntime builds a runtime with no transport: the waiter tests drive
// the read function directly and only need the polling knobs.
func newWaiterRuntime() *Runtime {
	return &Runtime{pollInterval: time.Millisecond, lifecycleTimeout: time.Second}
}

// scriptedReads returns a readFunc that yields one entry per call and repeats
// the last entry once the script is exhausted.
func scriptedReads(script []struct {
	value VPC
	err   error
}) (readFunc[VPC], *int) {
	calls := 0
	return func(context.Context) (VPC, error) {
		entry := script[min(calls, len(script)-1)]
		calls++
		return entry.value, entry.err
	}, &calls
}

func TestWaitForPresentSkipsDeletingGeneration(t *testing.T) {
	t.Parallel()
	read, calls := scriptedReads([]struct {
		value VPC
		err   error
	}{
		{err: ErrResourceNotFound},
		{value: VPC{Name: "test-vpc", Status: "deleting"}},
		{value: VPC{Name: "test-vpc", Status: "active"}},
	})

	current, err := waitForPresent(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", read)
	if err != nil {
		t.Fatalf("waitForPresent() error = %v", err)
	}
	if current.Status != "active" {
		t.Fatalf("waitForPresent() status = %q, want active", current.Status)
	}
	if *calls != 3 {
		t.Fatalf("read calls = %d, want 3", *calls)
	}
}

func TestWaitForPresentReportsTerminalState(t *testing.T) {
	t.Parallel()
	read, _ := scriptedReads([]struct {
		value VPC
		err   error
	}{{value: VPC{Name: "test-vpc", Status: "error", LastError: "quota exceeded"}}})

	_, err := waitForPresent(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", read)
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("waitForPresent() error = %v, want the gateway lastError", err)
	}
}

func TestWaitForPresentStopsOnUnexpectedReadError(t *testing.T) {
	t.Parallel()
	read, calls := scriptedReads([]struct {
		value VPC
		err   error
	}{{err: errors.New("infra-gateway returned HTTP 503")}})

	_, err := waitForPresent(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", read)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("waitForPresent() error = %v, want the transport error", err)
	}
	if *calls != 1 {
		t.Fatalf("read calls = %d, want 1: a non-404 read must not be retried", *calls)
	}
}

func TestWaitUntilPollsForTheCallerGoal(t *testing.T) {
	t.Parallel()
	read, _ := scriptedReads([]struct {
		value VPC
		err   error
	}{
		{value: VPC{Name: "test-vpc", Status: "pending"}},
		{value: VPC{Name: "test-vpc", Status: "active"}},
	})

	current, err := waitUntil(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", read,
		func(value VPC) bool { return value.Status == "active" }, "become active")
	if err != nil {
		t.Fatalf("waitUntil() error = %v", err)
	}
	if current.Status != "active" {
		t.Fatalf("waitUntil() status = %q, want active", current.Status)
	}
}

func TestWaitUntilSurfacesTheGoalOnDeadline(t *testing.T) {
	t.Parallel()
	read, _ := scriptedReads([]struct {
		value VPC
		err   error
	}{{value: VPC{Name: "test-vpc", Status: "pending"}}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := waitUntil(ctx, newWaiterRuntime(), "VPC", "test-vpc", read,
		func(value VPC) bool { return value.Status == "active" }, "become active")
	if err == nil || !strings.Contains(err.Error(), "become active") {
		t.Fatalf("waitUntil() error = %v, want the goal in the deadline message", err)
	}
}

func TestWaitForAbsentRequiresAnAuthoritative404(t *testing.T) {
	t.Parallel()
	read, calls := scriptedReads([]struct {
		value VPC
		err   error
	}{
		{value: VPC{Name: "test-vpc", Status: "deleting"}},
		{err: ErrResourceNotFound},
	})

	if err := waitForAbsent(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", read); err != nil {
		t.Fatalf("waitForAbsent() error = %v", err)
	}
	if *calls != 2 {
		t.Fatalf("read calls = %d, want 2", *calls)
	}
}

func TestWaitForAbsentDoesNotTreatAReadFailureAsDeletion(t *testing.T) {
	t.Parallel()
	read, _ := scriptedReads([]struct {
		value VPC
		err   error
	}{{err: errors.New("infra-gateway returned HTTP 500")}})

	err := waitForAbsent(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", read)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("waitForAbsent() error = %v, want the transport error", err)
	}
}

func TestCreateWithNameReservationRetriesTheStaleReservation(t *testing.T) {
	t.Parallel()
	creates := 0
	create := func(context.Context) error {
		creates++
		if creates == 1 {
			return ErrResourceConflict
		}
		return nil
	}
	read, _ := scriptedReads([]struct {
		value VPC
		err   error
	}{
		{err: ErrResourceNotFound},
		{value: VPC{Name: "test-vpc", Status: "active"}},
	})

	current, err := createWithNameReservation(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", create, read)
	if err != nil {
		t.Fatalf("createWithNameReservation() error = %v", err)
	}
	if current.Status != "active" {
		t.Fatalf("createWithNameReservation() status = %q, want active", current.Status)
	}
	if creates != 2 {
		t.Fatalf("create calls = %d, want 2", creates)
	}
}

func TestCreateWithNameReservationWaitsOutADeletingGeneration(t *testing.T) {
	t.Parallel()
	creates := 0
	create := func(context.Context) error {
		creates++
		if creates == 1 {
			return ErrResourceConflict
		}
		return nil
	}
	read, _ := scriptedReads([]struct {
		value VPC
		err   error
	}{
		{value: VPC{Name: "test-vpc", Status: "deleting"}},
		{err: ErrResourceNotFound},
		{value: VPC{Name: "test-vpc", Status: "active"}},
	})

	if _, err := createWithNameReservation(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", create, read); err != nil {
		t.Fatalf("createWithNameReservation() error = %v", err)
	}
	if creates != 2 {
		t.Fatalf("create calls = %d, want 2", creates)
	}
}

func TestCreateWithNameReservationReportsAGenuineCollision(t *testing.T) {
	t.Parallel()
	creates := 0
	create := func(context.Context) error {
		creates++
		return ErrResourceConflict
	}
	read, _ := scriptedReads([]struct {
		value VPC
		err   error
	}{{value: VPC{Name: "test-vpc", Status: "active"}}})

	_, err := createWithNameReservation(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", create, read)
	if !errors.Is(err, ErrResourceConflict) {
		t.Fatalf("createWithNameReservation() error = %v, want ErrResourceConflict", err)
	}
	if creates != 1 {
		t.Fatalf("create calls = %d, want 1: a live resource is not a stale reservation", creates)
	}
}

func TestCreateWithNameReservationPassesThroughOtherErrors(t *testing.T) {
	t.Parallel()
	create := func(context.Context) error { return fmt.Errorf("infra-gateway returned HTTP 500") }
	read, calls := scriptedReads([]struct {
		value VPC
		err   error
	}{{value: VPC{Name: "test-vpc", Status: "active"}}})

	_, err := createWithNameReservation(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", create, read)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("createWithNameReservation() error = %v, want the transport error", err)
	}
	if *calls != 0 {
		t.Fatalf("read calls = %d, want 0: an untyped failure must not be retried", *calls)
	}
}

func TestDeleteAndWaitAcceptsAnAlreadyAbsentResource(t *testing.T) {
	t.Parallel()
	read, calls := scriptedReads([]struct {
		value VPC
		err   error
	}{{value: VPC{Name: "test-vpc", Status: "active"}}})
	remove := func(context.Context) error { return ErrResourceNotFound }

	if err := deleteAndWait(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", remove, read); err != nil {
		t.Fatalf("deleteAndWait() error = %v", err)
	}
	if *calls != 0 {
		t.Fatalf("read calls = %d, want 0", *calls)
	}
}

func TestDeleteAndWaitWaitsForAbsence(t *testing.T) {
	t.Parallel()
	read, calls := scriptedReads([]struct {
		value VPC
		err   error
	}{
		{value: VPC{Name: "test-vpc", Status: "deleting"}},
		{err: ErrResourceNotFound},
	})

	if err := deleteAndWait(context.Background(), newWaiterRuntime(), "VPC", "test-vpc", func(context.Context) error { return nil }, read); err != nil {
		t.Fatalf("deleteAndWait() error = %v", err)
	}
	if *calls != 2 {
		t.Fatalf("read calls = %d, want 2", *calls)
	}
}
