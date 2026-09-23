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
	// These statuses mean the requested volume change has not settled.
	statusVolumeCreating = "creating"
	statusVolumeResizing = "resizing"
)

// Volume is keyed by name; the gateway carries its UUID in the URN.
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

func (v Volume) lifecycle() (string, string) { return v.Status, v.LastError }

func (v Volume) tags() []Tag { return v.Tags }

// CreateVolume waits for provisioning. Duplicate names return an untyped 500, which cannot
// safely be retried because the create may have partially succeeded.
func (c *Runtime) CreateVolume(ctx context.Context, request CreateVolumeRequest) (Volume, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.post(operationCtx, svcInfraGateway, "/volumes/", request); err != nil {
		return Volume{}, err
	}
	return waitUntil(operationCtx, c, volumeKind, request.Name, c.volumeReader(request.Name), func(current Volume) bool {
		return !strings.EqualFold(current.Status, statusDeleting) && !strings.EqualFold(current.Status, statusVolumeCreating)
	}, "finish provisioning")
}

// GetVolume reads one volume by name.
func (c *Runtime) GetVolume(ctx context.Context, name string) (Volume, error) {
	var result Volume
	err := c.get(ctx, svcInfraGateway, volumePath(name), &result)
	return result, err
}

// ResizeVolume waits for the requested byte count, allowing equivalent quantity spellings.
// The gateway clears its resize latch after success or failure.
func (c *Runtime) ResizeVolume(ctx context.Context, name, size string) (Volume, error) {
	target, err := ParseQuantity(size)
	if err != nil {
		return Volume{}, fmt.Errorf("resize volume %q: %w", name, err)
	}

	operationCtx, cancel := context.WithTimeout(ctx, c.lifecycleTimeout)
	defer cancel()

	if err := c.post(operationCtx, svcInfraGateway, volumePath(name)+"/resize", resizeVolumeRequest{Size: size}); err != nil {
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

// UpdateVolumeTags patches and reads back tags. The gateway reports a tag-limit violation
// as an untyped 500.
func (c *Runtime) UpdateVolumeTags(ctx context.Context, name string, current, desired []Tag) (Volume, error) {
	return reconcileTags(ctx, c, volumeKind, name, volumePath(name), current, desired, c.volumeReader(name))
}

// DeleteVolume waits for deletion. Attached volumes return ErrResourceConflict and must be
// detached separately.
func (c *Runtime) DeleteVolume(ctx context.Context, name string) error {
	return deleteAndWait(ctx, c, volumeKind, name, func(ctx context.Context) error {
		return c.delete(ctx, svcInfraGateway, volumePath(name))
	}, c.volumeReader(name))
}

func (c *Runtime) volumeReader(name string) readFunc[Volume] {
	return func(ctx context.Context) (Volume, error) { return c.GetVolume(ctx, name) }
}

func volumePath(name string) string {
	return "/volumes/" + escapedPathSegment(name)
}

// Supported whole-number quantity suffixes, with binary units before decimal units.
var quantityUnits = []struct {
	suffix     string
	multiplier int64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30},
	{"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
	{"k", 1e3}, {"M", 1e6}, {"G", 1e9},
	{"T", 1e12}, {"P", 1e15}, {"E", 1e18},
}

// ParseQuantity converts whole-number byte quantities such as 10Gi or 500M to int64.
// Fractional quantities are unsupported.
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
