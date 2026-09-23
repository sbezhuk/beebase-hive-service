package apiaryclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	appreport "github.com/sbezhuk/beebase-hive-service/internal/application/report"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/apiaryclient"
)

func TestClient_Verify_OwnedAndWritable(t *testing.T) {
	apiaryID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/apiaries/"+apiaryID.String() {
			t.Errorf("path = %q, want to include the apiary id", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"writable": true})
	}))
	defer srv.Close()

	client := apiaryclient.New(srv.URL)
	writable, err := client.Verify(context.Background(), "good-token", apiaryID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !writable {
		t.Error("writable = false, want true")
	}
}

func TestInternalClient_GetDisplayInfoUsesServiceTokenAndTypedProjection(t *testing.T) {
	apiaryID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer internal-secret" {
			t.Errorf("Authorization = %q, want internal bearer token", got)
		}
		if r.URL.Path != "/internal/api/v1/apiaries/"+apiaryID.String()+"/display-info" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": apiaryID, "name": "Home Apiary"})
	}))
	defer srv.Close()

	client := apiaryclient.NewInternal(srv.URL, "internal-secret")
	got, err := client.GetDisplayInfo(context.Background(), apiaryID)
	if err != nil {
		t.Fatalf("GetDisplayInfo: %v", err)
	}
	if got != (appreport.ApiaryDisplayInfo{ID: apiaryID, Name: "Home Apiary"}) {
		t.Fatalf("display info = %+v", got)
	}
}

func TestClient_Verify_OwnedButReadOnly(t *testing.T) {
	apiaryID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"writable": false})
	}))
	defer srv.Close()

	client := apiaryclient.New(srv.URL)
	writable, err := client.Verify(context.Background(), "good-token", apiaryID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if writable {
		t.Error("writable = true, want false")
	}
}

func TestClient_Verify_NotOwned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := apiaryclient.New(srv.URL)
	_, err := client.Verify(context.Background(), "some-token", uuid.New())
	if !errors.Is(err, apphive.ErrApiaryNotFound) {
		t.Fatalf("Verify against a 404: got %v, want ErrApiaryNotFound", err)
	}
}

func TestClient_Verify_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := apiaryclient.New(srv.URL)
	_, err := client.Verify(context.Background(), "some-token", uuid.New())
	if err == nil {
		t.Fatal("Verify against a 500: got nil error, want a failure")
	}
	if errors.Is(err, apphive.ErrApiaryNotFound) {
		t.Fatal("Verify against a 500 should not be reported as ErrApiaryNotFound: that would mask apiary-service being broken as a plain 404")
	}
}

func TestClient_Verify_UnreachableServer(t *testing.T) {
	client := apiaryclient.New("http://127.0.0.1:1") // nothing listens here
	_, err := client.Verify(context.Background(), "some-token", uuid.New())
	if err == nil {
		t.Fatal("Verify against an unreachable server: got nil error, want a failure")
	}
}

func TestClient_WritableApiaryID_Restricted(t *testing.T) {
	apiaryID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apiaries/writable" {
			t.Errorf("path = %q, want /api/v1/apiaries/writable", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"unrestricted": false, "apiaryId": apiaryID})
	}))
	defer srv.Close()

	client := apiaryclient.New(srv.URL)
	id, unrestricted, err := client.WritableApiaryID(context.Background(), "token")
	if err != nil {
		t.Fatalf("WritableApiaryID: %v", err)
	}
	if unrestricted {
		t.Error("unrestricted = true, want false")
	}
	if id == nil || *id != apiaryID {
		t.Fatalf("apiary id = %v, want %s", id, apiaryID)
	}
}

func TestClient_WritableApiaryID_Unrestricted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"unrestricted": true, "apiaryId": nil})
	}))
	defer srv.Close()

	client := apiaryclient.New(srv.URL)
	id, unrestricted, err := client.WritableApiaryID(context.Background(), "token")
	if err != nil {
		t.Fatalf("WritableApiaryID: %v", err)
	}
	if !unrestricted {
		t.Error("unrestricted = false, want true")
	}
	if id != nil {
		t.Errorf("apiary id = %v, want nil", id)
	}
}

func TestClient_WritableApiaryID_NoApiaries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"unrestricted": false, "apiaryId": nil})
	}))
	defer srv.Close()

	client := apiaryclient.New(srv.URL)
	id, unrestricted, err := client.WritableApiaryID(context.Background(), "token")
	if err != nil {
		t.Fatalf("WritableApiaryID: %v", err)
	}
	if unrestricted {
		t.Error("unrestricted = true, want false")
	}
	if id != nil {
		t.Errorf("apiary id = %v, want nil", id)
	}
}
