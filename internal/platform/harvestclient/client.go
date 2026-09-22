package harvestclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	appreport "github.com/sbezhuk/beebase-hive-service/internal/application/report"
)

const reportRequestTimeout = 5 * time.Second

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client { return &Client{base: base, http: &http.Client{}} }

// ReportClient reads the authenticated internal report-data contract owned by
// harvest-service. It uses a bounded client and the service token, unlike the
// existing end-user-token delete client.
type ReportClient struct {
	base  string
	token string
	http  *http.Client
}

func NewInternal(base, internalToken string) *ReportClient {
	return &ReportClient{base: base, token: internalToken, http: &http.Client{Timeout: reportRequestTimeout}}
}

func (c *ReportClient) GetReportData(ctx context.Context, hiveID uuid.UUID, from, to time.Time) (appreport.HarvestReportResponse, error) {
	query := url.Values{}
	query.Set("from", from.Format("2006-01-02"))
	query.Set("to", to.Format("2006-01-02"))
	endpoint := fmt.Sprintf("%s/internal/api/v1/hives/%s/report-data?%s", c.base, hiveID, query.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return appreport.HarvestReportResponse{}, fmt.Errorf("harvest report client: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return appreport.HarvestReportResponse{}, fmt.Errorf("harvest report client: call harvest-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return appreport.HarvestReportResponse{}, fmt.Errorf("harvest report client: unexpected status %d", resp.StatusCode)
	}
	var value appreport.HarvestReportResponse
	if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
		return appreport.HarvestReportResponse{}, fmt.Errorf("harvest report client: decode response: %w", err)
	}
	if value.From != from.Format("2006-01-02") || value.To != to.Format("2006-01-02") || value.Count < 0 || value.Count != len(value.Harvests) {
		return appreport.HarvestReportResponse{}, fmt.Errorf("harvest report client: invalid response contract")
	}
	return value, nil
}

func (c *Client) DeleteByHive(ctx context.Context, accessToken string, hiveID uuid.UUID) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/api/v1/hives/%s/harvests", c.base, hiveID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("harvestclient: call harvest-service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("harvestclient: unexpected status %d", resp.StatusCode)
	}
	return nil
}
