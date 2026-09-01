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
	"github.com/sbezhuk/beebase-hive-service/internal/platform/inspectionclient"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/mediaclient"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
	transporthttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http"
	hivehttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/hive"

	"github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/jwks"
	"github.com/sbezhuk/beebase-common/logger"
	"github.com/sbezhuk/beebase-common/pagination"
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

// fakeCascadeTarget stands in for inspection-service's or media-service's
// delete endpoint: it always succeeds (204) and records every request it
// received, so tests can assert hive-service's cascade actually reached
// it, without running a second full service.
type fakeCascadeTarget struct {
	mu       sync.Mutex
	received []*http.Request
}

func newFakeCascadeTarget() *fakeCascadeTarget {
	return &fakeCascadeTarget{}
}

func (f *fakeCascadeTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.received = append(f.received, r.Clone(r.Context()))
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// calledForPath reports whether any received request's path matches want
// exactly.
func (f *fakeCascadeTarget) calledForPath(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.received {
		if r.URL.Path == want {
			return true
		}
	}
	return false
}

// calledWithQuery reports whether any received request's query string
// matches want exactly (order-independent, via url.Values equality).
func (f *fakeCascadeTarget) calledWithQuery(ownerType, ownerID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.received {
		if r.URL.Query().Get("owner_type") == ownerType && r.URL.Query().Get("owner_id") == ownerID {
			return true
		}
	}
	return false
}

type testStack struct {
	server      *httptest.Server
	apiary      *fakeApiaryService
	inspections *fakeCascadeTarget
	media       *fakeCascadeTarget
	priv        ed25519.PrivateKey
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

	inspections := newFakeCascadeTarget()
	inspectionServer := httptest.NewServer(inspections)
	t.Cleanup(inspectionServer.Close)

	media := newFakeCascadeTarget()
	mediaServer := httptest.NewServer(media)
	t.Cleanup(mediaServer.Close)

	hiveRepo := repopostgres.NewHiveRepository(tx)
	apiaryVerifier := apiaryclient.New(apiaryServer.URL)
	inspectionDeleter := inspectionclient.New(inspectionServer.URL)
	mediaDeleter := mediaclient.New(mediaServer.URL)
	hiveService := apphive.NewService(hiveRepo, apiaryVerifier, inspectionDeleter, mediaDeleter)
	log := logger.New("development", "error")
	handler := hivehttp.NewHandler(hiveService, log)

	router := transporthttp.NewRouter(log, pool, handler, verifier)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &testStack{server: srv, apiary: apiary, inspections: inspections, media: media, priv: priv}
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
	var list pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list: got %d hives, want 1", len(list.Items))
	}
	if list.Pagination.Total != 1 || list.Pagination.Page != 1 || list.Pagination.Limit != pagination.DefaultLimit {
		t.Fatalf("list: pagination = %+v, want total=1 page=1 limit=%d", list.Pagination, pagination.DefaultLimit)
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
	var list pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &list)
	if len(list.Items) != 0 {
		t.Fatalf("other user's list = %v, want empty", list.Items)
	}
	if list.Pagination.Total != 0 {
		t.Fatalf("other user's list total = %d, want 0", list.Pagination.Total)
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

func TestHiveFlow_ListPagination(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	for i := 0; i < 3; i++ {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiaryID.String(),
			"name":      "H",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d: status = %d, want %d", i, resp.StatusCode, http.StatusCreated)
		}
	}

	resp := stack.request(t, http.MethodGet, "/api/v1/hives?page=1&limit=2", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var page pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &page)
	if len(page.Items) != 2 {
		t.Fatalf("list page 1: got %d items, want 2", len(page.Items))
	}
	if page.Pagination.Total != 3 || page.Pagination.TotalPages != 2 || !page.Pagination.HasNext || page.Pagination.HasPrevious {
		t.Fatalf("list page 1: pagination = %+v, want total=3 total_pages=2 has_next=true has_previous=false", page.Pagination)
	}
}

func TestHiveFlow_ListInvalidPageAndLimit(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	cases := []string{
		"/api/v1/hives?page=0",
		"/api/v1/hives?page=-1",
		"/api/v1/hives?page=abc",
		"/api/v1/hives?limit=0",
		"/api/v1/hives?limit=101",
		"/api/v1/hives?limit=abc",
	}
	for _, path := range cases {
		resp := stack.request(t, http.MethodGet, path, token, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want %d", path, resp.StatusCode, http.StatusBadRequest)
		}
	}
}

// TestHiveFlow_DeleteCascadesInspectionsAndMedia is the end-to-end proof
// that deleting a hive reaches inspection-service and media-service
// before hard-deleting the hive itself, exercised over real HTTP.
func TestHiveFlow_DeleteCascadesInspectionsAndMedia(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": apiaryID.String(),
		"name":      "Gone soon",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created hivehttp.Response
	decodeJSON(t, resp, &created)

	resp = stack.request(t, http.MethodDelete, "/api/v1/hives/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	wantInspectionsPath := "/api/v1/hives/" + created.ID.String() + "/inspections"
	if !stack.inspections.calledForPath(wantInspectionsPath) {
		t.Errorf("delete did not cascade to inspection-service at %s", wantInspectionsPath)
	}
	if !stack.media.calledWithQuery("HIVE", created.ID.String()) {
		t.Errorf("delete did not cascade to media-service for owner_type=HIVE owner_id=%s", created.ID)
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/hives/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestHiveFlow_DeleteByApiary is the end-to-end proof of the cascade
// primitive apiary-service calls when it deletes an apiary: every hive
// under the apiary is fully cascade-deleted, while a hive under a
// different apiary survives.
func TestHiveFlow_DeleteByApiary(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	otherApiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	var ids []uuid.UUID
	for _, name := range []string{"H1", "H2"} {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiaryID.String(),
			"name":      name,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want %d", name, resp.StatusCode, http.StatusCreated)
		}
		var created hivehttp.Response
		decodeJSON(t, resp, &created)
		ids = append(ids, created.ID)
	}

	otherToken := stack.tokenFor(t, userID)
	stack.apiary.allow(otherToken, otherApiaryID)
	resp := stack.request(t, http.MethodPost, "/api/v1/hives", otherToken, map[string]string{
		"apiary_id": otherApiaryID.String(),
		"name":      "Keep",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create keep: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var keep hivehttp.Response
	decodeJSON(t, resp, &keep)

	resp = stack.request(t, http.MethodDelete, "/api/v1/hives?apiary_id="+apiaryID.String(), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DeleteByApiary: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	for _, id := range ids {
		resp := stack.request(t, http.MethodGet, "/api/v1/hives/"+id.String(), token, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("get %s after DeleteByApiary: status = %d, want %d", id, resp.StatusCode, http.StatusNotFound)
		}
		if !stack.media.calledWithQuery("HIVE", id.String()) {
			t.Errorf("DeleteByApiary did not cascade to media-service for hive %s", id)
		}
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/hives/"+keep.ID.String(), otherToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get hive under a different apiary after DeleteByApiary: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
