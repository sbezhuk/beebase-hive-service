package subscriptionclient_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sbezhuk/beebase-hive-service/internal/platform/subscriptionclient"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClient_GetEntitlement_Success(t *testing.T) {
	cases := []struct {
		name        string
		entitlement string
	}{
		{"free tier", "free"},
		{"pro tier", "pro"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := &http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet {
						t.Errorf("method = %s, want GET", r.Method)
					}
					if r.URL.Path != "/api/v1/subscription" {
						t.Errorf("path = %s, want /api/v1/subscription", r.URL.Path)
					}
					if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
						t.Errorf("auth = %s, want Bearer test-token", auth)
					}

					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewBufferString(fmt.Sprintf(`{"entitlement":%q}`, tc.entitlement))),
						Header:     make(http.Header),
					}, nil
				}),
			}

			client := subscriptionclient.NewWithHTTPClient("http://subscription-service", httpClient)
			ent, err := client.GetEntitlement(context.Background(), "test-token")

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ent != tc.entitlement {
				t.Fatalf("entitlement = %s, want %s", ent, tc.entitlement)
			}
		})
	}
}

func TestClient_GetEntitlement_Non200(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewBufferString(`internal error`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	client := subscriptionclient.NewWithHTTPClient("http://subscription-service", httpClient)
	_, err := client.GetEntitlement(context.Background(), "test-token")

	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("error %q does not contain 'unexpected status 500'", err.Error())
	}
}

func TestClient_GetEntitlement_MalformedJSON(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`not-json`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	client := subscriptionclient.NewWithHTTPClient("http://subscription-service", httpClient)
	_, err := client.GetEntitlement(context.Background(), "test-token")

	if err == nil {
		t.Fatal("expected error for malformed json, got nil")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("error %q does not contain 'decode response'", err.Error())
	}
}
