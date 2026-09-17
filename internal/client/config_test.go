package client

import (
	"testing"
	"time"
)

func TestNewConfig(t *testing.T) {
	t.Parallel()

	config, err := NewConfig(
		"https://api.example.com/",
		"tenant-a",
		"https://auth.example.com/base/",
		"realm-a",
		"client-a",
		"secret-a",
		0,
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if got, want := config.Endpoint.String(), "https://api.example.com"; got != want {
		t.Fatalf("Endpoint = %q, want %q", got, want)
	}
	if got, want := config.Timeout, 30*time.Second; got != want {
		t.Fatalf("Timeout = %s, want %s", got, want)
	}
}

func TestNewConfigRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint  string
		namespace string
		authURL   string
		timeout   time.Duration
	}{
		"relative endpoint": {endpoint: "/api", namespace: "tenant", authURL: "https://auth.example.com"},
		"URL credentials":   {endpoint: "https://user@example.com", namespace: "tenant", authURL: "https://auth.example.com"},
		"empty namespace":   {endpoint: "https://api.example.com", namespace: "", authURL: "https://auth.example.com"},
		"negative timeout":  {endpoint: "https://api.example.com", namespace: "tenant", authURL: "https://auth.example.com", timeout: -time.Second},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewConfig(test.endpoint, test.namespace, test.authURL, "realm", "client", "secret", test.timeout)
			if err == nil {
				t.Fatal("NewConfig() error = nil, want validation error")
			}
		})
	}
}
