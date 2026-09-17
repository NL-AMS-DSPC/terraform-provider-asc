package client

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	volumeKind = "volume"
	// statusVolumeCreating and statusVolumeResizing are the two transient
	// statuses a volume passes through while a command of its own is still in
	// flight. Both mean the read model does not yet describe what was asked for.
	statusVolumeCreating = "creating"
	statusVolumeResizing = "resizing"
)

// Volume is the infra-gateway read model of a block volume.
//
// There is no id field: infra-gateway identifies a volume by name and carries
// the uuid inside the urn, so the provider keys everything off the name the way
// the virtual machine resource does.
type Volume struct {
	URN          string            `json:"urn"`
	Name         string            `json:"name"`
	Size         string            `json:"size"`
	StorageClass string            `json:"storageClass"`
	AccessMode   string            `json:"accessMode"`
	VolumeMode   string            `json:"volumeMode"`
	Status       string            `json:"status"`
	LastError    string            `json:"lastError"`
	TenantID     string            `json:"tenantId"`
	AttachedToVM string            `json:"attachedToVM"`
	Tags         []Tag             `json:"tags"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
}

// CreateVolumeRequest is the body infra-gateway accepts on POST /volumes/.
type CreateVolumeRequest struct {
	Name         string `json:"name"`
	Size         string `json:"size"`
	StorageClass string `json:"storageClass"`
	AccessMode   string `json:"accessMode"`
	Tags         []Tag  `json:"tags,omitempty"`
}

type resizeVolumeRequest struct {
	Size string `json:"size"`
}

// lifecycle and tags implement resourceView so the shared waiters in wait.go
// and the tag reconciler in tags.go serve volumes too. They live here rather
// than in models.go because the volume model itself is declared here.

func (v Volume) lifecycle() (string, string) { return v.Status, v.LastError }

func (v Volume) tags() []Tag { return v.Tags }

// CreateVolume creates a volume and waits until the gateway reports it settled.
//
// createWithNameReservation is deliberately not used. infra-gateway's
// respondWithServiceError has no branch for domain.ErrBlockVolumeNameNotUnique,
// so a duplicate volume name arrives as an untyped HTTP 500 instead of the
// typed 409 the shared helper retries on. An untyped 500 can equally mean the
// create partially succeeded, which makes a retry unsafe, so the error is
// reported verbatim and the operator decides what to do with it.
func (c *Runtime) CreateVolume(ctx context.Context, request CreateVolumeRequest) (Volume, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.post(operationCtx, "/volumes/", request); err != nil {
		return Volume{}, err
	}
	return waitUntil(operationCtx, c, volumeKind, request.Name, c.volumeReader(request.Name), func(current Volume) bool {
		return !strings.EqualFold(current.Status, statusDeleting) && !strings.EqualFold(current.Status, statusVolumeCreating)
	}, "finish provisioning")
}

// GetVolume reads one volume by name.
func (c *Runtime) GetVolume(ctx context.Context, name string) (Volume, error) {
	var result Volume
	err := c.get(ctx, volumePath(name), &result)
	return result, err
}

// ResizeVolume grows a volume in place and waits for the read model to report
// the new size.
//
// Unlike a virtual machine resize, the request_block_volume_resizing slice
// clears its latch on both the acknowledged and the failed event, so a resize
// is genuinely repeatable and can be modelled as an in-place update. The wait
// compares byte counts rather than strings because the gateway may echo the
// size back in a different but numerically equal spelling.
func (c *Runtime) ResizeVolume(ctx context.Context, name, size string) (Volume, error) {
	target, err := ParseQuantity(size)
	if err != nil {
		return Volume{}, fmt.Errorf("resize volume %q: %w", name, err)
	}

	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.post(operationCtx, volumePath(name)+"/resize", resizeVolumeRequest{Size: size}); err != nil {
		return Volume{}, fmt.Errorf("resize volume %q: %w", name, err)
	}
	return waitUntil(operationCtx, c, volumeKind, name, c.volumeReader(name), func(current Volume) bool {
		if strings.EqualFold(current.Status, statusVolumeResizing) {
			return false
		}
		actual, parseErr := ParseQuantity(current.Size)
		return parseErr == nil && actual == target
	}, "report its new size")
}

// UpdateVolumeTags moves a volume's tags from current to desired.
//
// The gateway applies a patch rather than a replacement, so removing a tag
// needs an explicit empty-valued entry; reconcileTags computes that. Note that
// PatchVolume omits the conflict check the VPC, subnet and virtual machine
// patch handlers perform, so breaching the 25 tag limit surfaces as an untyped
// HTTP 500 rather than a 409.
func (c *Runtime) UpdateVolumeTags(ctx context.Context, name string, current, desired []Tag) (Volume, error) {
	return reconcileTags(ctx, c, volumeKind, name, volumePath(name), current, desired, c.volumeReader(name))
}

// DeleteVolume deletes a volume and waits for it to disappear. A volume still
// attached to a virtual machine is refused with ErrResourceConflict, which is
// reported as is: detaching is not the provider's decision to make.
func (c *Runtime) DeleteVolume(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, volumeKind, name, func(ctx context.Context) error {
		return c.delete(ctx, volumePath(name))
	}, c.volumeReader(name))
}

func (c *Runtime) volumeReader(name string) readFunc[Volume] {
	return func(ctx context.Context) (Volume, error) { return c.GetVolume(ctx, name) }
}

func volumePath(name string) string {
	return "/volumes/" + escapedPathSegment(name)
}

// quantityUnits are the suffixes infra-gateway forwards to Kubernetes, binary
// first so the listing reads in the order an operator thinks about them. The
// suffixes are unambiguous under a plain suffix match: no binary suffix ends in
// a decimal one.
var quantityUnits = []struct {
	suffix     string
	multiplier int64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30},
	{"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
	{"k", 1e3}, {"M", 1e6}, {"G", 1e9},
	{"T", 1e12}, {"P", 1e15}, {"E", 1e18},
}

// ParseQuantity converts a Kubernetes quantity string such as "10Gi" or "500M"
// into bytes.
//
// size is opaque to infra-gateway, which stores whatever spelling it was given
// and may echo back a different but numerically equal one. Comparing byte
// counts rather than strings is the only way the provider can tell a real
// resize from a respelling, so the parser is kept here in the client rather
// than pulling in a Kubernetes dependency for it.
func ParseQuantity(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("size must not be empty")
	}
	for _, unit := range quantityUnits {
		if digits, ok := strings.CutSuffix(trimmed, unit.suffix); ok {
			return scaleQuantity(value, digits, unit.multiplier)
		}
	}
	return scaleQuantity(value, trimmed, 1)
}

func scaleQuantity(original, digits string, multiplier int64) (int64, error) {
	amount, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || amount < 0 {
		return 0, fmt.Errorf("size %q is not a supported quantity: expected a whole number of bytes optionally followed by Ki, Mi, Gi, Ti, Pi, Ei, k, M, G, T, P or E", original)
	}
	if amount != 0 && multiplier > math.MaxInt64/amount {
		return 0, fmt.Errorf("size %q does not fit in a 64 bit byte count", original)
	}
	return amount * multiplier, nil
}
