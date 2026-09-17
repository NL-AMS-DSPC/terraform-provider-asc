package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTokenManagerRequestsAndCachesClientCredentialsToken(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got, want := r.URL.Path, "/realms/realm-a/protocol/openid-connect/token"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
		}
		for key, want := range map[string]string{
			"grant_type":    "client_credentials",
			"client_id":     "client-a",
			"client_secret": "secret-a",
		} {
			if got := r.Form.Get(key); got != want {
				t.Errorf("form %s = %q, want %q", key, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-a", "expires_in": 300})
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant-a", server.URL, "realm-a", "client-a", "secret-a", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	runtime := NewRuntime(config)

	for range 2 {
		token, err := runtime.auth.tokenFor(context.Background())
		if err != nil {
			t.Fatalf("tokenFor() error = %v", err)
		}
		if token != "token-a" {
			t.Fatalf("tokenFor() = %q, want token-a", token)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("token requests = %d, want 1", got)
	}
}

func TestTokenManagerRejectsInvalidTokenResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"","expires_in":300}`))
	}))
	t.Cleanup(server.Close)

	config, err := NewConfig(server.URL, "tenant-a", server.URL, "realm-a", "client-a", "secret-a", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if _, err := NewRuntime(config).auth.tokenFor(context.Background()); err == nil {
		t.Fatal("tokenFor() error = nil, want invalid response error")
	}
}
