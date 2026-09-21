package client

import "context"

// ListSKUs reads the infra-gateway SKU catalogue.
func (c *Runtime) ListSKUs(ctx context.Context) ([]SKU, error) {
	var skus []SKU
	if err := c.get(ctx, "/skus/", &skus); err != nil {
		return nil, err
	}
	return skus, nil
}
