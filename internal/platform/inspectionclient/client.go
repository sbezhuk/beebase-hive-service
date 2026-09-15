// Package inspectionclient implements
// application/hive.InspectionDeleter and
// application/hive.InspectionStatusProvider against the real
// inspection-service over HTTP.
package inspectionclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const requestTimeout = 5 * time.Second

// Client deletes every inspection belonging to a hive by calling
// inspection-service's DELETE /api/v1/hives/{hiveID}/inspections,
// forwarding the caller's own access token so inspection-service scopes
// the delete to the same user this service already verified owns the
// hive. It also implements InspectionStatusProvider (GET
// /api/v1/inspections/hive-status), used to filter hive listings by
// "needs inspection".
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client that calls inspection-service at baseURL (e.g.
// "http://inspection-service:8080").
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// DeleteByHive implements application/hive.InspectionDeleter.
func (c *Client) DeleteByHive(ctx context.Context, accessToken string, hiveID uuid.UUID) error {
	url := fmt.Sprintf("%s/api/v1/hives/%s/inspections", c.baseURL, hiveID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("inspectionclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("inspectionclient: call inspection-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	default:
		return fmt.Errorf("inspectionclient: unexpected status %d from inspection-service", resp.StatusCode)
	}
}

type hiveInspectionStatusItem struct {
	HiveID            uuid.UUID `json:"hiveId"`
	LatestInspectedAt time.Time `json:"latestInspectedAt"`
}

type hiveInspectionStatusResponse struct {
	ThresholdDays int                        `json:"thresholdDays"`
	Hives         []hiveInspectionStatusItem `json:"hives"`
}

// HiveInspectionStatus implements application/hive.InspectionStatusProvider
// by calling GET /api/v1/inspections/hive-status.
func (c *Client) HiveInspectionStatus(ctx context.Context, accessToken string) (map[uuid.UUID]time.Time, int, error) {
	url := fmt.Sprintf("%s/api/v1/inspections/hive-status", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("inspectionclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("inspectionclient: call inspection-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("inspectionclient: unexpected status %d from inspection-service", resp.StatusCode)
	}

	var body hiveInspectionStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, fmt.Errorf("inspectionclient: decode response: %w", err)
	}

	latestByHive := make(map[uuid.UUID]time.Time, len(body.Hives))
	for _, item := range body.Hives {
		latestByHive[item.HiveID] = item.LatestInspectedAt
	}

	return latestByHive, body.ThresholdDays, nil
}
