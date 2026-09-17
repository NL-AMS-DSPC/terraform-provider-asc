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

const infraGatewayPrefix = "/api/infra-gateway"

func (c *Runtime) get(ctx context.Context, path string, output any) error {
	return c.request(ctx, http.MethodGet, path, nil, output)
}

func (c *Runtime) post(ctx context.Context, path string, input any) error {
	return c.request(ctx, http.MethodPost, path, input, nil)
}

func (c *Runtime) delete(ctx context.Context, path string) error {
	return c.request(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Runtime) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode infra-gateway request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	target := *c.config.Endpoint
	target.Path = strings.TrimRight(target.Path, "/") + infraGatewayPrefix + path
	target.RawQuery = ""
	target.Fragment = ""

	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return fmt.Errorf("create infra-gateway request: %w", err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	token, err := c.auth.tokenFor(ctx)
	if err != nil {
		return fmt.Errorf("authenticate infra-gateway request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant", c.config.Namespace)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send infra-gateway request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrResourceNotFound
	}
	if resp.StatusCode == http.StatusConflict {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrResourceConflict
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("infra-gateway returned HTTP %d", resp.StatusCode)
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

func escapedPathSegment(value string) string {
	return url.PathEscape(value)
}
