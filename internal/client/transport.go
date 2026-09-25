package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// API requests use these prefixes; authentication uses the separate realm token endpoint.
const (
	infraGatewayPrefix = "/api/infra-gateway"
	uamPrefix          = "/api/uam"
	ipamPrefix         = "/api/ipam"
)

type svcType string

const (
	svcInfraGateway svcType = "infra-gateway"
	svcUAM          svcType = "uam"
	svcIPAM         svcType = "ipam"
)

// infra-gateway and UAM are reached through the generated clients (see generated.go).
// IPAM has no Terraform-facing ASC spec yet, so its bare JSON routes keep this helper.

func (c *Runtime) get(ctx context.Context, svc svcType, path string, output any) error {
	return c.request(ctx, svc, http.MethodGet, path, nil, output)
}

func (c *Runtime) request(ctx context.Context, svc svcType, method, path string, input, output any) error {
	if svc != svcIPAM {
		return fmt.Errorf("unknown service type: %s", svc)
	}
	resp, err := c.send(ctx, method, ipamPrefix+path, input, string(svc))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if err := statusError(resp, string(svc)); err != nil {
		return err
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return fmt.Errorf("decode %s response: %w", svc, err)
	}
	return nil
}

// send performs an authenticated request; callers decode the service-specific response.
func (c *Runtime) send(ctx context.Context, method, path string, input any, service string) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode %s request: %w", service, err)
		}
		body = bytes.NewReader(encoded)
	}

	target := *c.config.Endpoint
	target.RawPath = strings.TrimRight(target.EscapedPath(), "/") + path
	decodedPath, err := url.PathUnescape(target.RawPath)
	if err != nil {
		return nil, fmt.Errorf("decode %s request path: %w", service, err)
	}
	target.Path = decodedPath
	target.RawQuery = ""
	target.Fragment = ""

	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", service, err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	token, err := c.auth.tokenFor(ctx)
	if err != nil {
		return nil, fmt.Errorf("authenticate %s request: %w", service, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant", c.config.Namespace)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send %s request: %w", service, err)
	}
	return resp, nil
}
