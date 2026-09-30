package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/uam"
)

// infraAPI and uamAPI return the oapi-codegen clients. They build requests from the
// vendored ASC specs; responses are decoded here because several Swaggo @Success
// annotations do not describe the body the services actually send (see decodeData).
func (c *Runtime) infraAPI() *infra.Client {
	return &infra.Client{Server: serviceURL(c.config.Endpoint, infraGatewayPrefix), Client: c.httpClient}
}

func (c *Runtime) uamAPI() *uam.Client {
	return &uam.Client{Server: serviceURL(c.config.Endpoint, uamPrefix), Client: c.httpClient}
}

// serviceURL keeps an escaped endpoint path escaped exactly once; generated clients resolve
// operation paths relative to it.
func serviceURL(endpoint *url.URL, prefix string) string {
	base := *endpoint
	base.RawPath = strings.TrimRight(base.EscapedPath(), "/") + prefix + "/"
	base.Path = strings.TrimRight(base.Path, "/") + prefix + "/"
	base.RawQuery = ""
	base.Fragment = ""
	return base.String()
}

// authError marks a failure of the request editor so it is not reported as a send failure.
type authError struct {
	service svcType
	err     error
}

func (e *authError) Error() string {
	return fmt.Sprintf("authenticate %s request: %v", e.service, e.err)
}

func (e *authError) Unwrap() error { return e.err }

// authorize is the request editor for every generated call: bearer token and tenant.
func (c *Runtime) authorize(svc svcType) func(context.Context, *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		token, err := c.auth.tokenFor(ctx)
		if err != nil {
			return &authError{service: svc, err: err}
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Tenant", c.config.Namespace)
		return nil
	}
}

func (c *Runtime) infraEditor() infra.RequestEditorFn {
	return c.authorize(svcInfraGateway)
}

func (c *Runtime) uamEditor() uam.RequestEditorFn {
	return c.authorize(svcUAM)
}

// tenant is the X-Tenant value generated infra-gateway operations require as a parameter.
func (c *Runtime) tenant() string {
	return c.config.Namespace
}

// segmentError rejects a path parameter the generated clients cannot address. They
// resolve operation paths against the server URL, which removes "." and ".." segments, so
// a VPC named ".." would otherwise be deleted as DELETE /api/infra-gateway/ and its 404
// read as absence.
type segmentError struct {
	value string
}

func (e *segmentError) Error() string {
	return fmt.Sprintf("name %q cannot be used as a URL path segment", e.value)
}

func checkSegments(values ...string) error {
	for _, v := range values {
		if v == "." || v == ".." {
			return &segmentError{value: v}
		}
	}
	return nil
}

type generatedCall func(ctx context.Context) (*http.Response, error)

// callInfra sends a generated infra-gateway request and decodes the data envelope of a
// successful response into output. A nil output discards the body.
func callInfra[T any](ctx context.Context, call generatedCall, output *T) error {
	resp, err := send(ctx, svcInfraGateway, call)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return decodeData(resp.Body, output)
}

// callUAM sends a generated UAM request and decodes a successful bare JSON body into
// output. A nil output discards the body.
func callUAM[T any](ctx context.Context, call generatedCall, output *T) error {
	resp, err := send(ctx, svcUAM, call)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	var decoded *T
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("decode %s response: %w", svcUAM, err)
	}
	if decoded == nil {
		return fmt.Errorf("validate %s response: expected a non-null JSON value", svcUAM)
	}
	*output = *decoded
	return nil
}

// send runs the call and classifies the status; a non-nil response is always successful.
func send(ctx context.Context, svc svcType, call generatedCall) (*http.Response, error) {
	resp, err := call(ctx)
	if err != nil {
		var auth *authError
		if errors.As(err, &auth) {
			return nil, auth
		}
		var segment *segmentError
		if errors.As(err, &segment) {
			return nil, segment
		}
		return nil, fmt.Errorf("send %s request: %w", svc, err)
	}
	if err := statusError(resp, string(svc)); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// decodeData unwraps a {"data": ...} response envelope. The generated parsers are
// not used: ListSubnets, for one, is annotated as an array of envelopes while the gateway
// sends a single envelope around the array.
func decodeData[T any](body io.Reader, output *T) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s response wrapper: error decoding: %w", svcInfraGateway, err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("validate %s response wrapper: validation error: missing data", svcInfraGateway)
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return fmt.Errorf("decode %s response data: error unmarshalling: %w", svcInfraGateway, err)
	}
	return nil
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

// value dereferences an optional generated field; the specs mark almost every response
// field optional because ASC's validation tags drive required-ness.
func value[T any](field *T) T {
	var zero T
	if field == nil {
		return zero
	}
	return *field
}
