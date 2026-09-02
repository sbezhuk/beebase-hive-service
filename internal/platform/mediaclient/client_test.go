package mediaclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/mediaclient"
)

func TestClient_VerifyOwnership_Success(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/media" {
			t.Errorf("path = %q, want /api/v1/media", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		got := r.URL.Query()["ids"]
		if len(got) != 2 || got[0] != id1.String() || got[1] != id2.String() {
			t.Errorf("ids query = %v, want [%s, %s]", got, id1, id2)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{"id": id1}, {"id": id2}},
		})
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.VerifyOwnership(context.Background(), "good-token", []uuid.UUID{id1, id2}); err != nil {
		t.Fatalf("VerifyOwnership: %v", err)
	}
}

// TestClient_VerifyOwnership_FewerItemsThanRequestedFailsClosed proves that
// any requested id media-service's response silently omits (unknown,
// deleted, or someone else's) is treated as a rejection, not ignored.
func TestClient_VerifyOwnership_FewerItemsThanRequestedFailsClosed(t *testing.T) {
	owned := uuid.New()
	foreign := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{"id": owned}}, // foreign is silently omitted
		})
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	err := client.VerifyOwnership(context.Background(), "good-token", []uuid.UUID{owned, foreign})
	if !errors.Is(err, apphive.ErrImageNotFound) {
		t.Fatalf("VerifyOwnership with one id omitted: got %v, want ErrImageNotFound", err)
	}
}

func TestClient_VerifyOwnership_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.VerifyOwnership(context.Background(), "some-token", []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("VerifyOwnership against a 500: got nil error, want a failure")
	}
}

func TestClient_VerifyOwnership_UnreachableServer(t *testing.T) {
	client := mediaclient.New("http://127.0.0.1:1") // nothing listens here
	if err := client.VerifyOwnership(context.Background(), "some-token", []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("VerifyOwnership against an unreachable server: got nil error, want a failure")
	}
}

func TestClient_DeleteByIDs_Success(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		got := r.URL.Query()["ids"]
		if len(got) != 2 || got[0] != id1.String() || got[1] != id2.String() {
			t.Errorf("ids query = %v, want [%s, %s]", got, id1, id2)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.DeleteByIDs(context.Background(), "good-token", []uuid.UUID{id1, id2}); err != nil {
		t.Fatalf("DeleteByIDs: %v", err)
	}
}

func TestClient_DeleteByIDs_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.DeleteByIDs(context.Background(), "some-token", []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("DeleteByIDs against a 500: got nil error, want a failure")
	}
}

func TestClient_DeleteByIDs_UnreachableServer(t *testing.T) {
	client := mediaclient.New("http://127.0.0.1:1") // nothing listens here
	if err := client.DeleteByIDs(context.Background(), "some-token", []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("DeleteByIDs against an unreachable server: got nil error, want a failure")
	}
}
