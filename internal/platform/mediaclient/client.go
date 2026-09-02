// Package mediaclient implements application/hive.MediaClient against the
// real media-service over HTTP.
package mediaclient

import (
	"bytes"
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

// listPageLimit is the page size Client requests when walking every page
// of GET /api/v1/media?owner_type=...&owner_id=... to build a complete
// attached-media list: the maximum media-service allows, to keep the
// number of round trips as small as possible.
const listPageLimit = 100

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

// DeleteByOwner implements application/hive.MediaClient.
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

// mediaItem is the subset of media-service's MediaResponse this client
// cares about.
type mediaItem struct {
	ID        uuid.UUID `json:"id"`
	OwnerType string    `json:"owner_type"`
	OwnerID   uuid.UUID `json:"owner_id"`
}

// mediaPage is the subset of media-service's paginated list envelope this
// client cares about.
type mediaPage struct {
	Items      []mediaItem `json:"items"`
	Pagination struct {
		HasNext bool `json:"has_next"`
	} `json:"pagination"`
}

// ListAttached implements application/hive.MediaClient.
func (c *Client) ListAttached(ctx context.Context, accessToken string, hiveID uuid.UUID) ([]uuid.UUID, error) {
	ids := []uuid.UUID{}

	for page := 1; ; page++ {
		u := fmt.Sprintf("%s/api/v1/media?owner_type=HIVE&owner_id=%s&page=%d&limit=%d",
			c.baseURL, url.QueryEscape(hiveID.String()), page, listPageLimit)

		body, err := c.getPage(ctx, accessToken, u)
		if err != nil {
			return nil, err
		}

		for _, item := range body.Items {
			ids = append(ids, item.ID)
		}

		if !body.Pagination.HasNext {
			return ids, nil
		}
	}
}

func (c *Client) getPage(ctx context.Context, accessToken, u string) (*mediaPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("mediaclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mediaclient: call media-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mediaclient: unexpected status %d from media-service", resp.StatusCode)
	}

	var body mediaPage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("mediaclient: decode response: %w", err)
	}

	return &body, nil
}

// Attach implements application/hive.MediaClient by calling
// media-service's POST /api/v1/media/{id}/attach. media-service performs
// the actual ownership check and link; this is not a local verification.
func (c *Client) Attach(ctx context.Context, accessToken string, hiveID, mediaID uuid.UUID) error {
	u := fmt.Sprintf("%s/api/v1/media/%s/attach", c.baseURL, mediaID)

	body, err := json.Marshal(struct {
		OwnerType string `json:"owner_type"`
		OwnerID   string `json:"owner_id"`
	}{OwnerType: "HIVE", OwnerID: hiveID.String()})
	if err != nil {
		return fmt.Errorf("mediaclient: marshal attach body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mediaclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mediaclient: call media-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound, http.StatusConflict:
		// 404: mediaID doesn't exist, or doesn't belong to the caller.
		// 409: mediaID is already attached to a different owner - a
		// media item's owner is fixed the first time it's attached.
		// Both are indistinguishable to the caller, by the same
		// non-leaking convention hive.ErrNotFound already follows.
		return apphive.ErrImageNotFound
	default:
		return fmt.Errorf("mediaclient: unexpected status %d from media-service", resp.StatusCode)
	}
}

// Detach implements application/hive.MediaClient.
func (c *Client) Detach(ctx context.Context, accessToken string, mediaID uuid.UUID) error {
	u := fmt.Sprintf("%s/api/v1/media/%s", c.baseURL, mediaID)

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
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		// A 404 here means the media was already gone (e.g. a retried
		// reconciliation after a prior partial failure) - not an error,
		// same idempotent-delete contract media-service's own DELETE
		// endpoint documents.
		return nil
	default:
		return fmt.Errorf("mediaclient: unexpected status %d from media-service", resp.StatusCode)
	}
}
