package config

import (
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"DATABASE_URL":             "postgres://user:pass@localhost/db",
		"REDIS_ADDR":               "localhost:6379",
		"AUTH_JWKS_URL":            "http://auth-service:8080/.well-known/jwks.json",
		"INTERNAL_SERVICE_TOKEN":   "test-token",
		"PUBLIC_BASE_URL":          "http://localhost:8080",
		"APIARY_SERVICE_URL":       "http://apiary-service:8080",
		"INSPECTION_SERVICE_URL":   "http://inspection-service:8080",
		"HARVEST_SERVICE_URL":      "http://harvest-service:8080",
		"MEDIA_SERVICE_URL":        "http://media-service:8080",
		"SUBSCRIPTION_SERVICE_URL": "http://subscription-service:8080",
		"NOTIFICATION_SERVICE_URL": "http://notification-service:8080",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestLoadAcceptsValidNotificationServiceURL(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NotificationServiceURL != "http://notification-service:8080" {
		t.Fatalf("NotificationServiceURL = %q", cfg.NotificationServiceURL)
	}
}

func TestLoadRejectsInvalidNotificationServiceURL(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "missing", value: "", want: "NOTIFICATION_SERVICE_URL is required"},
		{name: "malformed", value: "://broken", want: "must be a valid HTTP or HTTPS URL"},
		{name: "relative", value: "/internal", want: "must be a valid HTTP or HTTPS URL"},
		{name: "wrong scheme", value: "ftp://notification-service:8080", want: "must be a valid HTTP or HTTPS URL"},
		{name: "missing host", value: "http://", want: "must be a valid HTTP or HTTPS URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("NOTIFICATION_SERVICE_URL", tt.value)

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}
