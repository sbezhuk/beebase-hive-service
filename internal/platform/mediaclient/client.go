// Package mediaclient implements application/hive.MediaDeleter against the
// real media-service over HTTP.
package mediaclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

const requestTimeout = 5 * time.Second

// Client deletes every media item attached to a hive by calling
// media-service's DELETE /api/v1/media?owner_type=HIVE&owner_id=...,
// forwarding the caller's own access token so media-service scopes the
// delete to the same user this service already verified owns the hive.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client that calls media-service at baseURL (e.g.
// "http://media-service:8080").
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// DeleteByOwner implements application/hive.MediaDeleter.
func (c *Client) DeleteByOwner(ctx context.Context, accessToken string, hiveID uuid.UUID) error {
	u := fmt.Sprintf("%s/api/v1/media?owner_type=HIVE&owner_id=%s", c.baseURL, url.QueryEscape(hiveID.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("mediaclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mediaclient: call media-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	default:
		return fmt.Errorf("mediaclient: unexpected status %d from media-service", resp.StatusCode)
	}
}
