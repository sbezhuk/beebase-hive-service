package harvestclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client { return &Client{base: base, http: &http.Client{}} }

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
