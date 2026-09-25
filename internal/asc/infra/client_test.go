package infra

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type countingTransport struct {
	calls int
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	return http.DefaultTransport.RoundTrip(r)
}

// The generated client keeps the server prefix, escapes path parameters as a single
// segment, sends the X-Tenant parameter, runs request editors and uses the injected client.
func TestGeneratedClientRequest(t *testing.T) {
	t.Parallel()

	var method, rawPath, tenant, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, rawPath = r.Method, r.URL.EscapedPath()
		tenant, authorization = r.Header.Get("X-Tenant"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"name": "a/b c", "status": "ready", "tags": []map[string]string{{"key": "env", "value": "prod"}},
		}})
	}))
	t.Cleanup(server.Close)

	transport := &countingTransport{}
	client, err := NewClientWithResponses(server.URL+"/api/infra-gateway",
		WithHTTPClient(&http.Client{Transport: transport}),
		WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer token")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	resp, err := client.GetVPCWithResponse(context.Background(), "a/b c", &GetVPCParams{XTenant: "tenant"})
	if err != nil {
		t.Fatalf("GetVPC: %v", err)
	}

	if got, want := method+" "+rawPath, "GET /api/infra-gateway/vpcs/a%2Fb%20c"; got != want {
		t.Errorf("request = %q, want %q", got, want)
	}
	if tenant != "tenant" || authorization != "Bearer token" {
		t.Errorf("X-Tenant = %q, Authorization = %q", tenant, authorization)
	}
	if transport.calls != 1 {
		t.Errorf("injected client served %d requests, want 1", transport.calls)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil || resp.JSON200.Data == nil {
		t.Fatalf("response = %d %+v", resp.StatusCode(), resp.JSON200)
	}
	vpc := resp.JSON200.Data
	if *vpc.Name != "a/b c" || *vpc.Status != "ready" || len(*vpc.Tags) != 1 || (*vpc.Tags)[0].Value != "prod" {
		t.Errorf("VPC = %+v", vpc)
	}
}

// A request body is encoded from the generated model as JSON.
func TestGeneratedClientBody(t *testing.T) {
	t.Parallel()

	var contentType string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := client.ResizeVolume(context.Background(), "data", &ResizeVolumeParams{XTenant: "tenant"},
		ResizeVolumeJSONRequestBody{Size: "20Gi"})
	if err != nil {
		t.Fatalf("ResizeVolume: %v", err)
	}
	_ = resp.Body.Close()

	if contentType != "application/json" || body["size"] != "20Gi" {
		t.Errorf("Content-Type = %q, body = %v", contentType, body)
	}
}
