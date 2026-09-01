package inspectionclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
