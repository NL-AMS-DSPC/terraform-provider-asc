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
)

func (c *Runtime) get(ctx context.Context, path string, output any) error {
	return c.request(ctx, http.MethodGet, path, nil, output)
}

func (c *Runtime) post(ctx context.Context, path string, input any) error {
	return c.request(ctx, http.MethodPost, path, input, nil)
}

func (c *Runtime) delete(ctx context.Context, path string) error {
	return c.request(ctx, http.MethodDelete, path, nil, nil)
}

// request talks to infra-gateway, whose successful bodies are wrapped in a
// "data" envelope.
func (c *Runtime) request(ctx context.Context, method, path string, input, output any) error {
	resp, err := c.send(ctx, method, infraGatewayPrefix+path, input, "infra-gateway")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if err := statusError(resp, "infra-gateway"); err != nil {
		return err
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode infra-gateway response envelope: %w", err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("decode infra-gateway response envelope: missing data")
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return fmt.Errorf("decode infra-gateway response data: %w", err)
	}
	return nil
}

func (c *Runtime) uamGet(ctx context.Context, path string, output any) error {
	return c.uamRequest(ctx, http.MethodGet, path, nil, output)
}

func (c *Runtime) uamPost(ctx context.Context, path string, input, output any) error {
	return c.uamRequest(ctx, http.MethodPost, path, input, output)
}

func (c *Runtime) uamPut(ctx context.Context, path string, input any) error {
	return c.uamRequest(ctx, http.MethodPut, path, input, nil)
}

func (c *Runtime) uamDelete(ctx context.Context, path string) error {
	return c.uamRequest(ctx, http.MethodDelete, path, nil, nil)
}

// uamRequest decodes bare JSON; UAM does not use the infra-gateway data envelope.
func (c *Runtime) uamRequest(ctx context.Context, method, path string, input, output any) error {
	resp, err := c.send(ctx, method, uamPrefix+path, input, "uam")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if err := statusError(resp, "uam"); err != nil {
		return err
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return fmt.Errorf("decode uam response: %w", err)
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
