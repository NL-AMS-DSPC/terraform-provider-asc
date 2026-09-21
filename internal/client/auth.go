package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const tokenRefreshMargin = 30 * time.Second

const (
	defaultLifecycleTimeout = 10 * time.Minute
	defaultPollInterval     = time.Second
)

type tokenManager struct {
	httpClient   *http.Client
	tokenURL     string
	clientID     string
	clientSecret string

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	now       func() time.Time
}

// Runtime is the configured in-memory provider client. Credentials and access
// tokens never enter Terraform resource state.
type Runtime struct {
	config           Config
	httpClient       *http.Client
	auth             *tokenManager
	lifecycleTimeout time.Duration
	pollInterval     time.Duration

	// tenantMu guards the lazily resolved tenant uuid, which UAM's role item
	// route needs. See Runtime.tenantUUID.
	tenantMu sync.Mutex
	tenantID string
}

// NewRuntime creates the shared authenticated runtime without fetching a token.
func NewRuntime(config Config) *Runtime {
	httpClient := &http.Client{Timeout: config.Timeout}
	tokenURL := *config.AuthURL
	tokenURL.RawPath = strings.TrimRight(tokenURL.EscapedPath(), "/") + "/realms/" + url.PathEscape(config.Organization) + "/protocol/openid-connect/token"
	tokenURL.Path = strings.TrimRight(tokenURL.Path, "/") + "/realms/" + config.Organization + "/protocol/openid-connect/token"

	return &Runtime{
		config:     config,
		httpClient: httpClient,
		auth: &tokenManager{
			httpClient:   httpClient,
			tokenURL:     tokenURL.String(),
			clientID:     config.ClientID,
			clientSecret: config.ClientSecret,
			now:          time.Now,
		},
		lifecycleTimeout: defaultLifecycleTimeout,
		pollInterval:     defaultPollInterval,
	}
}

func (m *tokenManager) tokenFor(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.token != "" && m.now().Add(tokenRefreshMargin).Before(m.expiresAt) {
		return m.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {m.clientID},
		"client_secret": {m.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create authentication request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request access token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("authentication service returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode authentication response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("authentication response did not include an access token")
	}
	if payload.ExpiresIn <= 0 {
		return "", fmt.Errorf("authentication response included an invalid token lifetime")
	}

	m.token = payload.AccessToken
	m.expiresAt = m.now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	return m.token, nil
}
