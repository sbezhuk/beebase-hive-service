// Package config loads hive-service configuration from environment
// variables, with sane defaults for local development.
package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all runtime configuration for the service.
type Config struct {
	Env string // "development" or "production"

	HTTPPort            string
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPIdleTimeout     time.Duration
	HTTPShutdownTimeout time.Duration

	DatabaseURL            string
	DatabaseConnectTimeout time.Duration

	LogLevel string // "debug", "info", "warn", "error"

	// AuthJWKSURL points at auth-service's public key endpoint
	// (GET /.well-known/jwks.json), used to verify access tokens without
	// ever holding a key that could mint one.
	AuthJWKSURL string

	// PublicBaseURL is the gateway's externally reachable base URL, used
	// to build the image_url for each entry in a response's `images`.
	// Unlike every other *_URL setting in this service, it must resolve
	// for the client, not just for server-to-server calls.
	PublicBaseURL string

	// ApiaryServiceURL is apiary-service's base URL. A hive can only be
	// created under an apiary the caller owns, and apiary-service is the
	// only source of truth for that: this service asks it, once, at
	// creation time.
	ApiaryServiceURL string

	// InspectionServiceURL and MediaServiceURL are inspection-service's and
	// media-service's base URLs. Deleting a hive cascades to its
	// inspections and media; this service asks each to delete everything
	// under the hive before hard-deleting it.
	InspectionServiceURL string
	MediaServiceURL      string
}

// Load builds a Config from environment variables, falling back to
// defaults suitable for local development where a variable is unset.
func Load() (*Config, error) {
	cfg := &Config{
		Env: getEnv("APP_ENV", "development"),

		HTTPPort:            getEnv("HTTP_PORT", "8080"),
		HTTPReadTimeout:     getDuration("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPWriteTimeout:    getDuration("HTTP_WRITE_TIMEOUT", 10*time.Second),
		HTTPIdleTimeout:     getDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		HTTPShutdownTimeout: getDuration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),

		DatabaseURL:            getEnv("DATABASE_URL", ""),
		DatabaseConnectTimeout: getDuration("DATABASE_CONNECT_TIMEOUT", 5*time.Second),

		LogLevel: getEnv("LOG_LEVEL", "info"),

		AuthJWKSURL:      getEnv("AUTH_JWKS_URL", ""),
		PublicBaseURL:    getEnv("PUBLIC_BASE_URL", ""),
		ApiaryServiceURL: getEnv("APIARY_SERVICE_URL", ""),

		InspectionServiceURL: getEnv("INSPECTION_SERVICE_URL", ""),
		MediaServiceURL:      getEnv("MEDIA_SERVICE_URL", ""),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required")
	}
	if cfg.AuthJWKSURL == "" {
		return nil, fmt.Errorf("config: AUTH_JWKS_URL is required")
	}
	if cfg.PublicBaseURL == "" {
		return nil, fmt.Errorf("config: PUBLIC_BASE_URL is required")
	}
	if cfg.ApiaryServiceURL == "" {
		return nil, fmt.Errorf("config: APIARY_SERVICE_URL is required")
	}
	if cfg.InspectionServiceURL == "" {
		return nil, fmt.Errorf("config: INSPECTION_SERVICE_URL is required")
	}
	if cfg.MediaServiceURL == "" {
		return nil, fmt.Errorf("config: MEDIA_SERVICE_URL is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
