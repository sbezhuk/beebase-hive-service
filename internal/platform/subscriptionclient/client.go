// Package subscriptionclient implements application/hive.EntitlementResolver
// against the subscription-service over HTTP.
package subscriptionclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const requestTimeout = 5 * time.Second

// Client calls subscription-service to resolve the caller's entitlement,
// forwarding their verified access token so subscription-service evaluates
// entitlement against the authenticated user.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client that calls subscription-service at baseURL (e.g.
// "http://subscription-service:8080").
func New(baseURL string) *Client {
	return NewWithHTTPClient(baseURL, &http.Client{Timeout: requestTimeout})
}

// NewWithHTTPClient returns a Client that uses the provided HTTP client.
func NewWithHTTPClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{
		baseURL: baseURL,
		http:    httpClient,
	}
}

type subscriptionResponse struct {
	Entitlement string `json:"entitlement"`
}

// GetEntitlement implements application/hive.EntitlementResolver.
func (c *Client) GetEntitlement(ctx context.Context, accessToken string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/subscription", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("subscriptionclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("subscriptionclient: call subscription-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subscriptionclient: unexpected status %d from subscription-service", resp.StatusCode)
	}

	var body subscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("subscriptionclient: decode response: %w", err)
	}

	return body.Entitlement, nil
}
