// Package mediaclient implements application/hive.MediaClient against the
// real media-service over HTTP.
package mediaclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
)

const requestTimeout = 5 * time.Second

// Client implements application/hive.MediaClient against the real
// media-service, forwarding the caller's own access token on every call
// so media-service scopes each operation to the same user this service
// already verified owns the hive.
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

// mediaListResponse is the subset of media-service's GET /api/v1/media
// response this client cares about.
type mediaListResponse struct {
	Items []struct {
		ID uuid.UUID `json:"id"`
	} `json:"items"`
}

// idsQuery builds the repeated ?ids=&ids=... query string shared by
// VerifyOwnership and DeleteByIDs.
func idsQuery(ids []uuid.UUID) string {
	q := url.Values{}
	for _, id := range ids {
		q.Add("ids", id.String())
	}
	return q.Encode()
}

// VerifyOwnership implements application/hive.MediaClient by calling
// media-service's GET /api/v1/media?ids= - the only remaining source of
// truth for "does this media id exist and belong to me". media-service
// silently omits any id that doesn't exist, was deleted, or belongs to a
// different user, so a response with fewer items than requested means at
// least one id failed that check.
func (c *Client) VerifyOwnership(ctx context.Context, accessToken string, ids []uuid.UUID) error {
	u := fmt.Sprintf("%s/api/v1/media?%s", c.baseURL, idsQuery(ids))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("mediaclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mediaclient: call media-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mediaclient: unexpected status %d from media-service", resp.StatusCode)
	}

	var body mediaListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("mediaclient: decode response: %w", err)
	}

	if len(body.Items) != len(ids) {
		// At least one requested id wasn't returned: unknown, deleted, or
		// belongs to a different user - indistinguishable to the caller,
		// by the same non-leaking convention hive.ErrNotFound follows.
		return apphive.ErrImageNotFound
	}

	return nil
}

// DeleteByIDs implements application/hive.MediaClient by calling
// media-service's DELETE /api/v1/media?ids= - used when the hive itself
// is being cascade-deleted, to hard-delete every media file it references.
func (c *Client) DeleteByIDs(ctx context.Context, accessToken string, ids []uuid.UUID) error {
	u := fmt.Sprintf("%s/api/v1/media?%s", c.baseURL, idsQuery(ids))

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
