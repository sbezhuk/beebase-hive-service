// Package apiaryclient implements application/hive.ApiaryVerifier against
// the real apiary-service over HTTP.
package apiaryclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
)

const requestTimeout = 5 * time.Second

// Client verifies apiary ownership by forwarding the caller's own access
// token to apiary-service's GET /api/v1/apiaries/{id}, and trusting
// apiary-service's own ownership check: a 200 means whoever holds that
// token owns that apiary, a 404 means they don't (or it doesn't exist).
// This service never queries apiary ownership itself.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client that calls apiary-service at baseURL (e.g.
// "http://apiary-service:8080").
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// apiaryDetailResponse decodes just the one field this client needs off
// apiary-service's GET /api/v1/apiaries/{id} response - it carries many
// more (name, location, images, ...) that this service has no use for.
type apiaryDetailResponse struct {
	Writable bool `json:"writable"`
}

// writableApiaryResponse decodes apiary-service's GET /api/v1/apiaries/
// writable response.
type writableApiaryResponse struct {
	Unrestricted bool       `json:"unrestricted"`
	ApiaryID     *uuid.UUID `json:"apiaryId"`
}

// Verify implements application/hive.ApiaryVerifier.
func (c *Client) Verify(ctx context.Context, accessToken string, apiaryID uuid.UUID) (bool, error) {
	url := fmt.Sprintf("%s/api/v1/apiaries/%s", c.baseURL, apiaryID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("apiaryclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("apiaryclient: call apiary-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		var body apiaryDetailResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return false, fmt.Errorf("apiaryclient: decode response: %w", err)
		}
		return body.Writable, nil
	case http.StatusNotFound:
		return false, apphive.ErrApiaryNotFound
	default:
		// Anything else (401, 5xx, ...) is unexpected for a token this
		// service already verified itself: fail closed with a distinct,
		// observable error rather than silently treating it as "not
		// found", which would mask a real problem (e.g. apiary-service
		// misconfigured or unreachable) as a client-facing 404.
		return false, fmt.Errorf("apiaryclient: unexpected status %d from apiary-service", resp.StatusCode)
	}
}

// WritableApiaryID implements application/hive.ApiaryVerifier.
func (c *Client) WritableApiaryID(ctx context.Context, accessToken string) (*uuid.UUID, bool, error) {
	url := fmt.Sprintf("%s/api/v1/apiaries/writable", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("apiaryclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("apiaryclient: call apiary-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("apiaryclient: unexpected status %d from apiary-service", resp.StatusCode)
	}

	var body writableApiaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, false, fmt.Errorf("apiaryclient: decode response: %w", err)
	}
	return body.ApiaryID, body.Unrestricted, nil
}
