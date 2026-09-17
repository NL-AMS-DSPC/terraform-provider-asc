package client

import "context"

// ListSKUs reads the authoritative SKU catalogue. It is read-only: the
// catalogue is served directly and has no create, delete or reservation
// lifecycle, so it needs none of the conflict retry handling the mutating
// infrastructure calls require. SKU is declared in models.go and is already
// shared with the virtual machine resource.
func (c *Runtime) ListSKUs(ctx context.Context) ([]SKU, error) {
	var skus []SKU
	if err := c.get(ctx, "/skus/", &skus); err != nil {
		return nil, err
	}
	return skus, nil
}
