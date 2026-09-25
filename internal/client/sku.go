package client

import (
	"context"
	"net/http"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
)

// ListSKUs reads the infra-gateway SKU catalogue.
func (c *Runtime) ListSKUs(ctx context.Context) ([]SKU, error) {
	var wire []infra.SKUResponse
	err := callInfra(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.infraAPI().ListSKUs(ctx, c.infraEditor())
	}, &wire)
	if err != nil {
		return nil, err
	}
	skus := make([]SKU, len(wire))
	for i := range wire {
		skus[i] = skuFromInfra(&wire[i])
	}
	return skus, nil
}
