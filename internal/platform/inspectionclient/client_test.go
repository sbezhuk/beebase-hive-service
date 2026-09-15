package inspectionclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/platform/inspectionclient"
)

func TestClient_DeleteByHive_Success(t *testing.T) {
	hiveID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/hives/"+hiveID.String()+"/inspections" {
			t.Errorf("path = %q, want to include the hive id", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := inspectionclient.New(srv.URL)
	if err := client.DeleteByHive(context.Background(), "good-token", hiveID); err != nil {
		t.Fatalf("DeleteByHive: %v", err)
	}
}

func TestClient_DeleteByHive_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := inspectionclient.New(srv.URL)
	if err := client.DeleteByHive(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByHive against a 500: got nil error, want a failure")
	}
}

func TestClient_DeleteByHive_UnreachableServer(t *testing.T) {
	client := inspectionclient.New("http://127.0.0.1:1") // nothing listens here
	if err := client.DeleteByHive(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByHive against an unreachable server: got nil error, want a failure")
	}
}

func TestClient_HiveInspectionStatus_Success(t *testing.T) {
	hiveID := uuid.New()
	latest := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/inspections/hive-status" {
			t.Errorf("path = %q, want /api/v1/inspections/hive-status", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"thresholdDays": 14,
			"hives": []map[string]any{
				{"hiveId": hiveID.String(), "latestInspectedAt": latest.Format(time.RFC3339)},
			},
		})
	}))
	defer srv.Close()

	client := inspectionclient.New(srv.URL)
	latestByHive, thresholdDays, err := client.HiveInspectionStatus(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("HiveInspectionStatus: %v", err)
	}
	if thresholdDays != 14 {
		t.Errorf("thresholdDays = %d, want 14", thresholdDays)
	}
	got, ok := latestByHive[hiveID]
	if !ok || !got.Equal(latest) {
		t.Errorf("latestByHive[hiveID] = %v, ok=%v, want %v", got, ok, latest)
	}
}

func TestClient_HiveInspectionStatus_EmptyHivesYieldsEmptyMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"thresholdDays": 14, "hives": []any{}})
	}))
	defer srv.Close()

	client := inspectionclient.New(srv.URL)
	latestByHive, thresholdDays, err := client.HiveInspectionStatus(context.Background(), "token")
	if err != nil {
		t.Fatalf("HiveInspectionStatus: %v", err)
	}
	if len(latestByHive) != 0 {
		t.Errorf("latestByHive = %+v, want empty", latestByHive)
	}
	if thresholdDays != 14 {
		t.Errorf("thresholdDays = %d, want 14", thresholdDays)
	}
}

func TestClient_HiveInspectionStatus_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := inspectionclient.New(srv.URL)
	if _, _, err := client.HiveInspectionStatus(context.Background(), "token"); err == nil {
		t.Fatal("HiveInspectionStatus against a 500: got nil error, want a failure")
	}
}

func TestClient_HiveInspectionStatus_UnreachableServer(t *testing.T) {
	client := inspectionclient.New("http://127.0.0.1:1") // nothing listens here
	if _, _, err := client.HiveInspectionStatus(context.Background(), "token"); err == nil {
		t.Fatal("HiveInspectionStatus against an unreachable server: got nil error, want a failure")
	}
}
