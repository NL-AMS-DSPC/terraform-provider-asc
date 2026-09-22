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

type responseWrapper interface {
	decode(body io.ReadCloser) error
	validate() error
	unmarshal(output any) error
}

type envelope struct {
	Data json.RawMessage `json:"data"`
}

func (e *envelope) decode(body io.ReadCloser) error {
	if err := json.NewDecoder(body).Decode(e); err != nil {
		return fmt.Errorf("error decoding: %w", err)
	}
	return nil
}

func (e *envelope) validate() error {
	if len(e.Data) == 0 || string(e.Data) == "null" {
		return fmt.Errorf("validation error: missing data")
	}
	return nil
}

func (e *envelope) unmarshal(output any) error {
	if err := json.Unmarshal(e.Data, output); err != nil {
		return fmt.Errorf("error unmarshalling: %w", err)
	}
	return nil
}

type service struct {
	name            string
	prefix          string
	responseWrapper responseWrapper
}

func serviceForName(svc svcType) service {
	switch svc {
	case svcUAM:
		return service{
			name:   string(svcUAM),
			prefix: uamPrefix,
		}
	case svcIPAM:
		return service{
			name:   string(svcIPAM),
			prefix: ipamPrefix,
		}
	default:
		return service{
			name:            string(svcInfraGateway),
			prefix:          infraGatewayPrefix,
			responseWrapper: &envelope{},
		}
	}
}

func (c *Runtime) get(ctx context.Context, path string, output any) error {
	return c.request(ctx, svcInfraGateway, http.MethodGet, path, nil, output)
}

func (c *Runtime) post(ctx context.Context, path string, input any) error {
	return c.request(ctx, svcInfraGateway, http.MethodPost, path, input, nil)
}

func (c *Runtime) patch(ctx context.Context, path string, input any) error {
	return c.request(ctx, svcInfraGateway, http.MethodPatch, path, input, nil)
}

func (c *Runtime) delete(ctx context.Context, path string) error {
	return c.request(ctx, svcInfraGateway, http.MethodDelete, path, nil, nil)
}

func (c *Runtime) getForSvc(ctx context.Context, svc svcType, path string, output any) error {
	return c.request(ctx, svc, http.MethodGet, path, nil, output)
}

func (c *Runtime) postForSvc(ctx context.Context, svc svcType, path string, input any) error {
	return c.request(ctx, svc, http.MethodPost, path, input, nil)
}

func (c *Runtime) putForSvc(ctx context.Context, svc svcType, path string, input any) error {
	return c.request(ctx, svc, http.MethodPut, path, input, nil)
}

func (c *Runtime) deleteForSvc(ctx context.Context, svc svcType, path string) error {
	return c.request(ctx, svc, http.MethodDelete, path, nil, nil)
}

// request talks to infra-gateway, whose successful bodies are wrapped in a
// "data" envelope.
func (c *Runtime) request(ctx context.Context, svcName svcType, method, path string, input, output any) error {
	svc := serviceForName(svcName)
	resp, err := c.send(ctx, method, svc.prefix+path, input, svc.name)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if err := statusError(resp, svc.name); err != nil {
		return err
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if svc.responseWrapper == nil {
		if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
			return fmt.Errorf("decode %s response: %w", svc.name, err)
		}
		return nil
	}
	if err := svc.responseWrapper.decode(resp.Body); err != nil {
		return fmt.Errorf("decode %s response wrapper: %w", svc.name, err)
	}
	if err := svc.responseWrapper.validate(); err != nil {
		return fmt.Errorf("validate %s response wrapper: %w", svc.name, err)
	}
	if err := svc.responseWrapper.unmarshal(output); err != nil {
		return fmt.Errorf("decode %s response data: %w", svc.name, err)
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

// Only 404 proves absence. UAM also returns 502 for upstream failures, so it must remain
// an error.
func statusError(resp *http.Response, service string) error {
	switch {
	case resp.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrResourceNotFound
	case resp.StatusCode == http.StatusConflict:
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrResourceConflict
	case resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices:
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("%s returned HTTP %d", service, resp.StatusCode)
	}
	return nil
}

func escapedPathSegment(value string) string {
	return url.PathEscape(value)
}
