//go:build integration

package http_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/apiaryclient"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
	transporthttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http"
	hivehttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/hive"

	"github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/jwks"
	"github.com/sbezhuk/beebase-common/logger"
)

const testKID = "test-kid"

// fakeApiaryService stands in for the real apiary-service: it owns
// exactly one apiary per bearer token registered via allow, and answers
// GET /api/v1/apiaries/{id} exactly like the real service would - 200 if
// the presented token's owner owns that apiary, 404 otherwise - so this
// test exercises hive-service's real cross-service HTTP call without
// needing a second full service running.
type fakeApiaryService struct {
	mu    sync.Mutex
	owned map[string]uuid.UUID // "Bearer <token>" -> the one apiary it owns
}

func newFakeApiaryService() *fakeApiaryService {
	return &fakeApiaryService{owned: map[string]uuid.UUID{}}
}

func (f *fakeApiaryService) allow(token string, apiaryID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owned["Bearer "+token] = apiaryID
}

func (f *fakeApiaryService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	owned, ok := f.owned[r.Header.Get("Authorization")]
	f.mu.Unlock()

	apiaryID, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/api/v1/apiaries/"))
	if err != nil || !ok || owned != apiaryID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type testStack struct {
	server *httptest.Server
	apiary *fakeApiaryService
	priv   ed25519.PrivateKey
}

// newTestStack wires a full router against a real PostgreSQL database
// (every write scoped to a transaction rolled back at the end of the
// test), a real JWKS server, and a fake apiary-service - exactly
// mirroring how hive-service verifies tokens and apiary ownership in
// production, just with throwaway stand-ins instead of the real services.
func newTestStack(t *testing.T) *testStack {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping HTTP hive integration test")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	jwksHandler, err := jwks.NewHandler(pub, testKID)
	if err != nil {
		t.Fatalf("jwks.NewHandler: %v", err)
	}
	jwksServer := httptest.NewServer(jwksHandler)
	t.Cleanup(jwksServer.Close)

	verifier, err := authmw.NewVerifierFromJWKSURL(context.Background(), jwksServer.URL)
	if err != nil {
		t.Fatalf("NewVerifierFromJWKSURL: %v", err)
	}

	apiary := newFakeApiaryService()
	apiaryServer := httptest.NewServer(apiary)
	t.Cleanup(apiaryServer.Close)

	hiveRepo := repopostgres.NewHiveRepository(tx)
	apiaryVerifier := apiaryclient.New(apiaryServer.URL)
	hiveService := apphive.NewService(hiveRepo, apiaryVerifier)
	log := logger.New("development", "error")
	handler := hivehttp.NewHandler(hiveService, log)

	router := transporthttp.NewRouter(log, pool, handler, verifier)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &testStack{server: srv, apiary: apiary, priv: priv}
}

func (s *testStack) tokenFor(t *testing.T, userID uuid.UUID) string {
	t.Helper()

	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = testKID

	signed, err := token.SignedString(s.priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func (s *testStack) request(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, s.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

func TestHiveFlow_CreateGetListUpdateDelete(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	// Create
	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": apiaryID.String(),
		"name":      "Hive 1",
		"location":  "North corner",
		"notes":     "strong colony",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created hivehttp.Response
	decodeJSON(t, resp, &created)
	if created.ApiaryID != apiaryID {
		t.Fatalf("create: apiary_id = %s, want %s", created.ApiaryID, apiaryID)
	}

	// Get
	resp = stack.request(t, http.MethodGet, "/api/v1/hives/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// List
	resp = stack.request(t, http.MethodGet, "/api/v1/hives", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var list []hivehttp.Response
	decodeJSON(t, resp, &list)
	if len(list) != 1 {
		t.Fatalf("list: got %d hives, want 1", len(list))
	}

	// Update
	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+created.ID.String(), token, map[string]string{
		"name":     "Renamed hive",
		"location": "South corner",
		"notes":    "moved",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var updated hivehttp.Response
	decodeJSON(t, resp, &updated)
	if updated.Name != "Renamed hive" {
		t.Fatalf("update: name = %q, want %q", updated.Name, "Renamed hive")
	}
	if updated.ApiaryID != apiaryID {
		t.Fatalf("update: apiary_id changed to %s, want unchanged %s", updated.ApiaryID, apiaryID)
	}

	// Delete
	resp = stack.request(t, http.MethodDelete, "/api/v1/hives/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	// Get after delete: gone
	resp = stack.request(t, http.MethodGet, "/api/v1/hives/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestHiveFlow_CreateRejectedWhenApiaryNotOwned is the end-to-end proof
// that a hive can't be created under an apiary the caller doesn't own,
// verified against a real cross-service HTTP call.
func TestHiveFlow_CreateRejectedWhenApiaryNotOwned(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())
	someoneElsesApiary := uuid.New()
	// Deliberately not calling stack.apiary.allow for this token/apiary pair.

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": someoneElsesApiary.String(),
		"name":      "Squatter hive",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("create under unowned apiary: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "apiary_not_found" {
		t.Fatalf("error code = %v, want apiary_not_found", errBody["code"])
	}
}

// TestHiveFlow_CannotAccessAnotherUsersHive is the end-to-end proof of
// this module's central requirement, exercised over real HTTP with real
// JWT verification for two different users.
func TestHiveFlow_CannotAccessAnotherUsersHive(t *testing.T) {
	stack := newTestStack(t)
	owner := uuid.New()
	other := uuid.New()
	apiaryID := uuid.New()
	ownerToken := stack.tokenFor(t, owner)
	otherToken := stack.tokenFor(t, other)
	stack.apiary.allow(ownerToken, apiaryID)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", ownerToken, map[string]string{
		"apiary_id": apiaryID.String(),
		"name":      "Owner's hive",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created hivehttp.Response
	decodeJSON(t, resp, &created)

	cases := []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPut, map[string]string{"name": "Hijacked"}},
		{http.MethodDelete, nil},
	}
	for _, tc := range cases {
		resp := stack.request(t, tc.method, "/api/v1/hives/"+created.ID.String(), otherToken, tc.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s as a different user: status = %d, want %d", tc.method, resp.StatusCode, http.StatusNotFound)
		}
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/hives", otherToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var list []hivehttp.Response
	decodeJSON(t, resp, &list)
	if len(list) != 0 {
		t.Fatalf("other user's list = %v, want empty", list)
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/hives/"+created.ID.String(), ownerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner get after other user's attempts: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var stillOwners hivehttp.Response
	decodeJSON(t, resp, &stillOwners)
	if stillOwners.Name != "Owner's hive" {
		t.Fatalf("name = %q after other user's attempts, want unchanged", stillOwners.Name)
	}
}

func TestHiveFlow_WithoutTokenIsUnauthorized(t *testing.T) {
	stack := newTestStack(t)

	resp := stack.request(t, http.MethodGet, "/api/v1/hives", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("list without token: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestHiveFlow_ValidationErrors(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": uuid.New().String(),
		"name":      "",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with empty name: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": "not-a-uuid",
		"name":      "ok",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with malformed apiary_id: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/hives/not-a-uuid", token, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("get with malformed hive id: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}
