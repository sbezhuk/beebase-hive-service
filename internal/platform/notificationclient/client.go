package notificationclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"time"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 3 * time.Second}}
}
func (c *Client) Cleanup(ctx context.Context, t string, id uuid.UUID) error {
	b, _ := json.Marshal(map[string]any{"entities": []map[string]any{{"type": t, "id": id}}})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/api/v1/reminders/cleanup", bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("notification cleanup status %d", resp.StatusCode)
	}
	return nil
}
