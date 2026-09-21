package client

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// Config contains provider endpoints, credentials and the request timeout.
type Config struct {
	Endpoint     *url.URL
	Namespace    string
	AuthURL      *url.URL
	Organization string
	ClientID     string
	ClientSecret string
	Timeout      time.Duration
}

// NewConfig validates provider configuration without making a network call.
func NewConfig(endpoint, namespace, authURL, organization, clientID, clientSecret string, timeout time.Duration) (Config, error) {
	apiEndpoint, err := parseHTTPURL("endpoint", endpoint)
	if err != nil {
		return Config{}, err
	}

	authEndpoint, err := parseHTTPURL("auth_url", authURL)
	if err != nil {
		return Config{}, err
	}

	values := map[string]string{
		"namespace": namespace,
		"org":       organization,
		"username":  clientID,
		"password":  clientSecret,
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s must be configured", name)
		}
	}

	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < 0 {
		return Config{}, fmt.Errorf("timeout must be greater than zero")
	}

	return Config{
		Endpoint:     apiEndpoint,
		Namespace:    strings.TrimSpace(namespace),
		AuthURL:      authEndpoint,
		Organization: strings.TrimSpace(organization),
		ClientID:     strings.TrimSpace(clientID),
		ClientSecret: clientSecret,
		Timeout:      timeout,
	}, nil
}

func parseHTTPURL(name, value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s must be configured", name)
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%s must be an absolute HTTP or HTTPS URL", name)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("%s must not include credentials", name)
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}
