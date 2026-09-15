package notificationclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCleanupUsesInternalToken(t *testing.T) {
	var got string
	c := New("http://notification-service", "internal-test-token")
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Get("Authorization")
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
	})
	if err := c.Cleanup(context.Background(), "hive", uuid.New()); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer internal-test-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
