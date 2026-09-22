package inspectionclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReportClient_SendsInternalTokenAndDateRange(t *testing.T) {
	hiveID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/api/v1/hives/"+hiveID.String()+"/report-data" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer internal-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.URL.Query().Get("from"); got != "2026-01-01" {
			t.Fatalf("from = %q", got)
		}
		if got := r.URL.Query().Get("to"); got != "2027-01-01" {
			t.Fatalf("to = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"from":"2026-01-01","to":"2027-01-01","inspections":[],"colonyHealth":{"state":"UNKNOWN"},"healthHistory":{"from":"2026-01-01","to":"2027-01-01","points":[]}}`))
	}))
	defer server.Close()

	client := NewInternal(server.URL, "internal-token")
	value, err := client.GetReportData(context.Background(), hiveID, date(t, "2026-01-01"), date(t, "2027-01-01"))
	if err != nil {
		t.Fatalf("GetReportData() error = %v", err)
	}
	if value.ColonyHealth.State != "UNKNOWN" {
		t.Fatalf("health state = %q", value.ColonyHealth.State)
	}
}

func TestReportClient_RejectsUnexpectedStatusAndMalformedJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{name: "status", body: "downstream detail", code: http.StatusBadGateway},
		{name: "json", body: "not-json", code: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, err := NewInternal(server.URL, "token").GetReportData(context.Background(), uuid.New(), time.Time{}, time.Time{})
			if err == nil || strings.Contains(err.Error(), tc.body) {
				t.Fatalf("error = %v, must be non-nil without downstream body", err)
			}
		})
	}
}

func date(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
