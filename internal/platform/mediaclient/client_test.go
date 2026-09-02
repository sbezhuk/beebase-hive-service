package mediaclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/mediaclient"
)

func TestClient_DeleteByOwner_Success(t *testing.T) {
	hiveID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/media" {
			t.Errorf("path = %q, want /api/v1/media", r.URL.Path)
		}
		if got := r.URL.Query().Get("owner_type"); got != "HIVE" {
			t.Errorf("owner_type = %q, want HIVE", got)
		}
		if got := r.URL.Query().Get("owner_id"); got != hiveID.String() {
			t.Errorf("owner_id = %q, want %s", got, hiveID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.DeleteByOwner(context.Background(), "good-token", hiveID); err != nil {
		t.Fatalf("DeleteByOwner: %v", err)
	}
}

func TestClient_DeleteByOwner_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.DeleteByOwner(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByOwner against a 500: got nil error, want a failure")
	}
}

func TestClient_DeleteByOwner_UnreachableServer(t *testing.T) {
	client := mediaclient.New("http://127.0.0.1:1") // nothing listens here
	if err := client.DeleteByOwner(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByOwner against an unreachable server: got nil error, want a failure")
	}
}

func TestClient_ListAttached_WalksEveryPage(t *testing.T) {
	hiveID := uuid.New()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("owner_type"); got != "HIVE" {
			t.Errorf("owner_type = %q, want HIVE", got)
		}
		if got := r.URL.Query().Get("owner_id"); got != hiveID.String() {
			t.Errorf("owner_id = %q, want %s", got, hiveID)
		}

		page := r.URL.Query().Get("page")
		var items []map[string]any
		hasNext := false
		switch page {
		case "1", "":
			items = []map[string]any{{"id": ids[0]}, {"id": ids[1]}}
			hasNext = true
		case "2":
			items = []map[string]any{{"id": ids[2]}}
			hasNext = false
		default:
			t.Fatalf("unexpected page %q", page)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      items,
			"pagination": map[string]any{"has_next": hasNext},
		})
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	got, err := client.ListAttached(context.Background(), "good-token", hiveID)
	if err != nil {
		t.Fatalf("ListAttached: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("ListAttached returned %d ids, want %d", len(got), len(ids))
	}
	for i, id := range ids {
		if got[i] != id {
			t.Errorf("ListAttached[%d] = %s, want %s", i, got[i], id)
		}
	}
}

func TestClient_ListAttached_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if _, err := client.ListAttached(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("ListAttached against a 500: got nil error, want a failure")
	}
}

func TestClient_Attach_Success(t *testing.T) {
	hiveID := uuid.New()
	mediaID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != fmt.Sprintf("/api/v1/media/%s/attach", mediaID) {
			t.Errorf("path = %q, want /api/v1/media/%s/attach", r.URL.Path, mediaID)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["owner_type"] != "HIVE" {
			t.Errorf("owner_type = %q, want HIVE", body["owner_type"])
		}
		if body["owner_id"] != hiveID.String() {
			t.Errorf("owner_id = %q, want %s", body["owner_id"], hiveID)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": mediaID, "owner_type": "HIVE", "owner_id": hiveID,
		})
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.Attach(context.Background(), "good-token", hiveID, mediaID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
}

func TestClient_Attach_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	err := client.Attach(context.Background(), "some-token", uuid.New(), uuid.New())
	if !errors.Is(err, apphive.ErrImageNotFound) {
		t.Fatalf("Attach against 404: got %v, want ErrImageNotFound", err)
	}
}

func TestClient_Attach_AlreadyAttachedElsewhereTreatedAsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	err := client.Attach(context.Background(), "good-token", uuid.New(), uuid.New())
	if !errors.Is(err, apphive.ErrImageNotFound) {
		t.Fatalf("Attach against media attached elsewhere (409): got %v, want ErrImageNotFound", err)
	}
}

func TestClient_Detach_Success(t *testing.T) {
	mediaID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != fmt.Sprintf("/api/v1/media/%s", mediaID) {
			t.Errorf("path = %q, want /api/v1/media/%s", r.URL.Path, mediaID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.Detach(context.Background(), "good-token", mediaID); err != nil {
		t.Fatalf("Detach: %v", err)
	}
}

func TestClient_Detach_AlreadyGoneIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.Detach(context.Background(), "good-token", uuid.New()); err != nil {
		t.Fatalf("Detach against an already-gone media item: %v", err)
	}
}
