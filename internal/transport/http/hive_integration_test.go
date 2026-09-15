//go:build integration

package http_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
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
	"github.com/sbezhuk/beebase-hive-service/internal/platform/subscriptionclient"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
	transporthttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http"
	hivehttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/hive"

	"github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/jwks"
	"github.com/sbezhuk/beebase-common/logger"
	"github.com/sbezhuk/beebase-common/pagination"
)

const testKID = "test-kid"

// alwaysActiveSessionChecker is a stand-in for *sessionstore.Store: these
// integration tests mint tokens directly (see tokenFor) rather than going
// through auth-service's real session-issuing flow, so there's no actual
// session to track here - immediate cross-session revocation is exercised
// by auth-service's own integration test and by beebase-common/authmw's
// unit tests instead.
type alwaysActiveSessionChecker struct{}

func (alwaysActiveSessionChecker) IsActive(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return true, nil
}

// fakeApiaryService stands in for the real apiary-service: it owns
// exactly one apiary per bearer token registered via allow, and answers
// GET /api/v1/apiaries/{id} exactly like the real service would - 200
// (with a JSON body carrying "writable") if the presented token's owner
// owns that apiary, 404 otherwise - so this test exercises hive-service's
// real cross-service HTTP call without needing a second full service
// running. It also answers GET /api/v1/apiaries/writable, used by
// List/ListByApiary. allow() defaults a newly owned apiary to writable,
// matching the common case; lock() flips one to read-only for tests
// exercising the transitive parent-apiary check.
type fakeApiaryService struct {
	mu       sync.Mutex
	owned    map[string]map[uuid.UUID]bool // "Bearer <token>" -> apiaries it owns
	readOnly map[uuid.UUID]bool            // apiaries explicitly marked not writable
}

func newFakeApiaryService() *fakeApiaryService {
	return &fakeApiaryService{owned: map[string]map[uuid.UUID]bool{}, readOnly: map[uuid.UUID]bool{}}
}

func (f *fakeApiaryService) allow(token string, apiaryID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := "Bearer " + token
	if f.owned[key] == nil {
		f.owned[key] = map[uuid.UUID]bool{}
	}
	f.owned[key][apiaryID] = true
}

// lock marks apiaryID as currently read-only, as the real apiary-service
// would report once it falls outside the caller's Free entitlement.
func (f *fakeApiaryService) lock(apiaryID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readOnly[apiaryID] = true
}

func (f *fakeApiaryService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/apiaries/writable" {
		f.serveWritable(w, r)
		return
	}

	f.mu.Lock()
	owned := f.owned[r.Header.Get("Authorization")]
	f.mu.Unlock()

	apiaryID, err := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/api/v1/apiaries/"))
	if err != nil || owned == nil || !owned[apiaryID] {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	f.mu.Lock()
	isWritable := !f.readOnly[apiaryID]
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"writable": isWritable})
}

// serveWritable answers GET /api/v1/apiaries/writable: the first
// non-locked apiary owned by the caller, or {"unrestricted":false,
// "apiary_id":null} if none. Deterministic iteration order (sorted by
// string form) keeps tests reproducible.
func (f *fakeApiaryService) serveWritable(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	owned := f.owned[r.Header.Get("Authorization")]
	ids := make([]uuid.UUID, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	var apiaryID *uuid.UUID
	for _, id := range ids {
		if !f.readOnly[id] {
			idCopy := id
			apiaryID = &idCopy
			break
		}
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"unrestricted": false, "apiary_id": apiaryID})
}

// fakeCascadeTarget stands in for inspection-service's delete endpoint
// and, for media-service, the GET /api/v1/media?ids= and DELETE
// /api/v1/media?ids= endpoints hive-service now calls to verify image
// ownership on create/update and to hard-delete a hive's own files on
// cascade delete: it answers GET from an in-memory set of media ids a
// test can seed as belonging to the caller via own(), and 204 to
// everything else (including DELETE, so it still works as a plain
// cascade-delete stand-in for inspection-service). It records every
// request it received, so tests can assert hive-service's cascade
// actually reached it, without running a second full service.
type fakeCascadeTarget struct {
	mu       sync.Mutex
	received []*http.Request
	ownedIDs map[uuid.UUID]bool // mediaID -> belongs to the caller

	// hiveStatus/thresholdDays back GET /api/v1/inspections/hive-status
	// when this fake stands in for inspection-service - irrelevant when
	// it stands in for media-service instead.
	hiveStatus    map[uuid.UUID]time.Time
	thresholdDays int
}

func newFakeCascadeTarget() *fakeCascadeTarget {
	return &fakeCascadeTarget{ownedIDs: map[uuid.UUID]bool{}, hiveStatus: map[uuid.UUID]time.Time{}, thresholdDays: 14}
}

// setLatest registers hiveID's latest inspection date, so this fake's
// GET /api/v1/inspections/hive-status endpoint (when standing in for
// inspection-service) reports it - a hive never passed to setLatest is
// simply absent, exactly like a never-inspected hive would be.
func (f *fakeCascadeTarget) setLatest(hiveID uuid.UUID, latest time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hiveStatus[hiveID] = latest
}

// own registers each of ids as belonging to the caller, so this fake's GET
// /api/v1/media?ids= endpoint returns it - letting a test exercise
// hive-service's media-ownership verification without a real
// media-service.
func (f *fakeCascadeTarget) own(ids ...uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		f.ownedIDs[id] = true
	}
}

func (f *fakeCascadeTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.received = append(f.received, r.Clone(r.Context()))
	f.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/media":
		f.serveList(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/inspections/hive-status":
		f.serveHiveStatus(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// serveHiveStatus answers GET /api/v1/inspections/hive-status when this
// fake stands in for inspection-service.
func (f *fakeCascadeTarget) serveHiveStatus(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()

	type item struct {
		HiveID            uuid.UUID `json:"hive_id"`
		LatestInspectedAt time.Time `json:"latest_inspected_at"`
	}
	hives := make([]item, 0, len(f.hiveStatus))
	for id, latest := range f.hiveStatus {
		hives = append(hives, item{HiveID: id, LatestInspectedAt: latest})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"threshold_days": f.thresholdDays,
		"hives":          hives,
	})
}

// serveList answers GET /api/v1/media?ids=&ids=...: returns every
// requested id this fake was told is own()ed by the caller, silently
// omitting unknown/foreign ones - mirroring media-service's real
// behavior closely enough for hive-service's own ownership verification
// to be exercised against it.
func (f *fakeCascadeTarget) serveList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	items := []map[string]any{}
	for _, raw := range r.URL.Query()["ids"] {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		if f.ownedIDs[id] {
			items = append(items, map[string]any{"id": id})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
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

// calledWithQueryValue reports whether any received request's repeated
// query param key (e.g. ?ids=&ids=...) includes value among its values.
func (f *fakeCascadeTarget) calledWithQueryValue(key, value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.received {
		for _, v := range r.URL.Query()[key] {
			if v == value {
				return true
			}
		}
	}
	return false
}

type fakeSubscriptionTarget struct {
	mu          sync.Mutex
	entitlement string
	errStatus   int
}

func newFakeSubscriptionTarget() *fakeSubscriptionTarget {
	return &fakeSubscriptionTarget{entitlement: "pro"}
}

func (f *fakeSubscriptionTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.errStatus != 0 {
		w.WriteHeader(f.errStatus)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"entitlement": f.entitlement})
}

func (f *fakeSubscriptionTarget) setEntitlement(ent string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entitlement = ent
}

type testStack struct {
	server      *httptest.Server
	apiary      *fakeApiaryService
	inspections *fakeCascadeTarget
	media       *fakeCascadeTarget
	subs        *fakeSubscriptionTarget
	subServer   *httptest.Server
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

	verifier, err := authmw.NewVerifierFromJWKSURL(context.Background(), jwksServer.URL, alwaysActiveSessionChecker{})
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

	subs := newFakeSubscriptionTarget()
	subServer := httptest.NewServer(subs)
	t.Cleanup(subServer.Close)

	hiveRepo := repopostgres.NewHiveRepository(tx)
	apiaryVerifier := apiaryclient.New(apiaryServer.URL)
	inspectionDeleter := inspectionclient.New(inspectionServer.URL)
	mediaDeleter := mediaclient.New(mediaServer.URL)
	subClient := subscriptionclient.New(subServer.URL)
	hiveService := apphive.NewService(hiveRepo, apiaryVerifier, inspectionDeleter, inspectionDeleter, mediaDeleter, subClient)
	log := logger.New("development", "error")
	handler := hivehttp.NewHandler(hiveService, log, "http://localhost:8080")

	router := transporthttp.NewRouter(log, pool, handler, verifier)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &testStack{
		server:      srv,
		apiary:      apiary,
		inspections: inspections,
		media:       media,
		subs:        subs,
		subServer:   subServer,
		priv:        priv,
	}
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
		"name":  "Renamed hive",
		"notes": "moved",
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

func TestHiveFlow_NameUniqueness(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiary1 := uuid.New()
	apiary2 := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiary1)
	stack.apiary.allow(token, apiary2)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": apiary1.String(),
		"name":      "Hive 1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create first: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var first hivehttp.Response
	decodeJSON(t, resp, &first)

	resp = stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": apiary1.String(),
		"name":      "Hive 1",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("create duplicate in same apiary: status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &errBody)
	if errBody.Error.Code != hivehttp.CodeHiveNameExists {
		t.Fatalf("duplicate code = %q, want %q", errBody.Error.Code, hivehttp.CodeHiveNameExists)
	}

	resp = stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": apiary2.String(),
		"name":      "Hive 1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("same name in another apiary: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+first.ID.String(), token, map[string]string{
		"name":  "Hive 1",
		"notes": "unchanged-name update",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update unchanged name: status = %d, want %d", resp.StatusCode, http.StatusOK)
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
			"name":      fmt.Sprintf("H-%d", i),
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
		"/api/v1/hives?search=a",
		"/api/v1/hives?search=ab",
	}
	for _, path := range cases {
		resp := stack.request(t, http.MethodGet, path, token, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want %d", path, resp.StatusCode, http.StatusBadRequest)
		}
	}
}

func TestHiveFlow_ListByApiary_ValidAndExcludedOtherApiaries(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiary1 := uuid.New()
	apiary2 := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiary1)
	stack.apiary.allow(token, apiary2)

	// Create 2 hives in apiary1
	for _, name := range []string{"A1-Hive1", "A1-Hive2"} {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiary1.String(),
			"name":      name,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want %d", name, resp.StatusCode, http.StatusCreated)
		}
	}
	// Create 1 hive in apiary2
	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": apiary2.String(),
		"name":      "A2-Hive1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create A2-Hive1: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	// 1. Scoped listing returns only apiary1's hives
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiary1.String()+"/hives", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list by apiary1: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var page pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &page)
	if len(page.Items) != 2 {
		t.Fatalf("apiary1 hives: got %d items, want 2", len(page.Items))
	}
	if page.Pagination.Total != 2 {
		t.Fatalf("apiary1 total: got %d, want 2", page.Pagination.Total)
	}
	for _, h := range page.Items {
		if h.ApiaryID != apiary1 {
			t.Errorf("expected hive in apiary %s, got %s", apiary1, h.ApiaryID)
		}
	}

	// 2. Global listing GET /api/v1/hives remains unchanged and returns all 3 hives
	resp = stack.request(t, http.MethodGet, "/api/v1/hives", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("global list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var globalPage pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &globalPage)
	if len(globalPage.Items) != 3 || globalPage.Pagination.Total != 3 {
		t.Fatalf("global list: got %d items (total %d), want 3", len(globalPage.Items), globalPage.Pagination.Total)
	}
}

func TestHiveFlow_ListByApiary_Pagination(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	for i := 0; i < 5; i++ {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiaryID.String(),
			"name":      fmt.Sprintf("H-%d", i),
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d: status = %d, want %d", i, resp.StatusCode, http.StatusCreated)
		}
	}

	// Page 1 with limit 2
	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiaryID.String()+"/hives?page=1&limit=2", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 1: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var p1 pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &p1)
	if len(p1.Items) != 2 {
		t.Fatalf("page 1: got %d items, want 2", len(p1.Items))
	}
	if p1.Pagination.Total != 5 || p1.Pagination.TotalPages != 3 || !p1.Pagination.HasNext || p1.Pagination.HasPrevious {
		t.Fatalf("page 1 pagination: %+v", p1.Pagination)
	}

	// Page 3 with limit 2
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiaryID.String()+"/hives?page=3&limit=2", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 3: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var p3 pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &p3)
	if len(p3.Items) != 1 {
		t.Fatalf("page 3: got %d items, want 1", len(p3.Items))
	}
	if p3.Pagination.Total != 5 || p3.Pagination.TotalPages != 3 || p3.Pagination.HasNext || !p3.Pagination.HasPrevious {
		t.Fatalf("page 3 pagination: %+v", p3.Pagination)
	}
}

func TestHiveFlow_ListByApiary_Search(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	items := []map[string]string{
		{"name": "Queen Carniolan", "notes": "colony notes"},
		{"name": "Hive 2", "notes": "Queen spotted on frame 4"},
		{"name": "Worker Hive", "notes": "No queen yet"},
	}
	for _, item := range items {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiaryID.String(),
			"name":      item["name"],
			"notes":     item["notes"],
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
	}

	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiaryID.String()+"/hives?search=queen&page=1&limit=20", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var searchPage pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &searchPage)
	if len(searchPage.Items) != 3 || searchPage.Pagination.Total != 3 {
		t.Fatalf("search queen: got %d items (total %d), want 3", len(searchPage.Items), searchPage.Pagination.Total)
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiaryID.String()+"/hives?search=carniolan&page=1&limit=20", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search carniolan: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	decodeJSON(t, resp, &searchPage)
	if len(searchPage.Items) != 1 || searchPage.Pagination.Total != 1 {
		t.Fatalf("search carniolan: got %d items (total %d), want 1", len(searchPage.Items), searchPage.Pagination.Total)
	}
}

func TestHiveFlow_ListByApiary_InvalidAndNonexistentApiaryID(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	// Invalid UUID format -> 400 invalid_apiary_id
	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries/not-a-uuid/hives", token, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET not-a-uuid: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	var errResp map[string]any
	decodeJSON(t, resp, &errResp)
	errObj, _ := errResp["error"].(map[string]any)
	if errObj["code"] != hivehttp.CodeInvalidApiaryID {
		t.Errorf("expected error code %s, got %v", hivehttp.CodeInvalidApiaryID, errObj["code"])
	}

	// Nonexistent apiary -> 404 apiary_not_found
	randomApiary := uuid.New()
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+randomApiary.String()+"/hives", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET nonexistent apiary: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	decodeJSON(t, resp, &errResp)
	errObj, _ = errResp["error"].(map[string]any)
	if errObj["code"] != hivehttp.CodeApiaryNotFound {
		t.Errorf("expected error code %s, got %v", hivehttp.CodeApiaryNotFound, errObj["code"])
	}
}

func TestHiveFlow_ListByApiary_UnauthorizedAndCrossUser(t *testing.T) {
	stack := newTestStack(t)
	userA := uuid.New()
	userB := uuid.New()
	apiaryA := uuid.New()
	tokenA := stack.tokenFor(t, userA)
	tokenB := stack.tokenFor(t, userB)
	stack.apiary.allow(tokenA, apiaryA)

	// Unauthenticated request -> 401
	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiaryA.String()+"/hives", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated request: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	// Cross-user access (userB trying to list userA's apiary) -> 404 apiary_not_found
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiaryA.String()+"/hives", tokenB, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-user request: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	var errResp map[string]any
	decodeJSON(t, resp, &errResp)
	errObj, _ := errResp["error"].(map[string]any)
	if errObj["code"] != hivehttp.CodeApiaryNotFound {
		t.Errorf("expected error code %s, got %v", hivehttp.CodeApiaryNotFound, errObj["code"])
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
	photo := uuid.New()
	stack.media.own(photo)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID.String(),
		"name":      "Gone soon",
		"images":    []string{photo.String()},
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
	if !stack.media.calledWithQueryValue("ids", photo.String()) {
		t.Errorf("delete did not cascade to media-service for the hive's own image %s", photo)
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
	var photos []uuid.UUID
	for _, name := range []string{"H1", "H2"} {
		photo := uuid.New()
		stack.media.own(photo)
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
			"apiary_id": apiaryID.String(),
			"name":      name,
			"images":    []string{photo.String()},
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want %d", name, resp.StatusCode, http.StatusCreated)
		}
		var created hivehttp.Response
		decodeJSON(t, resp, &created)
		ids = append(ids, created.ID)
		photos = append(photos, photo)
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
	}
	for _, photo := range photos {
		if !stack.media.calledWithQueryValue("ids", photo.String()) {
			t.Errorf("DeleteByApiary did not cascade to media-service for image %s", photo)
		}
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/hives/"+keep.ID.String(), otherToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get hive under a different apiary after DeleteByApiary: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestHiveFlow_UpdateReplacesImages is the end-to-end proof of the images
// feature: a GET reports whatever hive-service itself persisted, an
// update that doesn't mention images leaves it alone, and an update with
// an explicit (deduplicated) images list replaces the set wholesale -
// without deleting the dropped id's underlying file - while rejecting
// IDs that don't belong to the caller.
func TestHiveFlow_UpdateReplacesImages(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	keep := uuid.New()
	drop := uuid.New()
	stack.media.own(keep, drop)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID.String(),
		"name":      "Hive 1",
		"images":    []string{keep.String(), drop.String()},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created hivehttp.Response
	decodeJSON(t, resp, &created)
	if len(created.Images) != 2 {
		t.Fatalf("create: images = %v, want 2 items", created.Images)
	}

	// Get reports both, without having been asked to change anything.
	resp = stack.request(t, http.MethodGet, "/api/v1/hives/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var fetched hivehttp.Response
	decodeJSON(t, resp, &fetched)
	if len(fetched.Images) != 2 {
		t.Fatalf("get: images = %v, want 2 items", fetched.Images)
	}

	// Update without an images field leaves both attached.
	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+created.ID.String(), token, map[string]string{"name": "Renamed"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update without images: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var untouched hivehttp.Response
	decodeJSON(t, resp, &untouched)
	if len(untouched.Images) != 2 {
		t.Fatalf("update without images: images = %v, want 2 items (untouched)", untouched.Images)
	}

	// Update with an explicit (deduplicated) images list prunes "drop".
	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+created.ID.String(), token, map[string]any{
		"name":   "Renamed again",
		"images": []string{keep.String(), keep.String()},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update with images: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var pruned hivehttp.Response
	decodeJSON(t, resp, &pruned)
	if len(pruned.Images) != 1 || pruned.Images[0].ID != keep {
		t.Fatalf("update with images: images = %v, want [%s]", pruned.Images, keep)
	}
	if stack.media.calledWithQueryValue("ids", drop.String()) {
		t.Errorf("dropping %s from images must not delete its underlying file (no DELETE call expected)", drop)
	}

	// An update referencing a media ID that doesn't belong to the caller
	// is rejected as a validation error.
	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+created.ID.String(), token, map[string]any{
		"name":   "Should not apply",
		"images": []string{uuid.New().String()},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update with foreign image: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestHiveFlow_CreateWithImages_RejectsForeignMedia proves Create
// validates every referenced image exactly like Update does, and - since
// there's no hive row yet to roll back - never persists one.
func TestHiveFlow_CreateWithImages_RejectsForeignMedia(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID.String(),
		"name":      "Hive 1",
		"images":    []string{uuid.New().String()},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with foreign image: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/hives", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var list pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &list)
	if len(list.Items) != 0 {
		t.Fatalf("a hive was persisted despite a rejected image: %v", list.Items)
	}
}

// TestHiveFlow_FreeTierLimit proves the 5-hive Free limit is enforced,
// end-to-end through the real HTTP handler, as a per-user, account-wide
// quota (FreeMaxHives) - not scoped to any single apiary: once the
// account holds 5 hives, a 6th is rejected regardless of which apiary
// (even a different, still-writable one) it targets. See
// application/hive.Service.Create's doc comment.
func TestHiveFlow_FreeTierLimit(t *testing.T) {
	stack := newTestStack(t)
	stack.subs.setEntitlement("free")

	userID := uuid.New()
	apiaryID1 := uuid.New()
	apiaryID2 := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID1)
	stack.apiary.allow(token, apiaryID2)

	// Fill apiary 1 up to the limit.
	for i := 1; i <= 5; i++ {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
			"apiary_id": apiaryID1.String(),
			"name":      fmt.Sprintf("Hive A%d", i),
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create hive %d in apiary 1: status = %d, want %d", i, resp.StatusCode, http.StatusCreated)
		}
	}

	// A 6th hive in the same (now full) apiary is rejected.
	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID1.String(),
		"name":      "Hive A6",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("6th hive create in apiary 1: status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "hive_limit_reached" {
		t.Fatalf("error code = %q, want %q", errBody.Error.Code, "hive_limit_reached")
	}

	// A hive in a second, otherwise-writable apiary is still rejected -
	// the limit is account-wide, not per-apiary: the account already
	// holds 5 hives regardless of which apiary they're in.
	resp = stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID2.String(),
		"name":      "Hive B1",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create hive in apiary 2 (account already at 5 hives in apiary 1): status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	decodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "hive_limit_reached" {
		t.Fatalf("error code = %q, want %q", errBody.Error.Code, "hive_limit_reached")
	}
}

// TestHiveFlow_FreeTierLimit_ParentApiaryReadOnly proves a Free user
// cannot create a hive under an apiary apiary-service reports as
// read-only, even though they own it - end-to-end through the real HTTP
// handler and the fake apiary-service's "writable" JSON field.
func TestHiveFlow_FreeTierLimit_ParentApiaryReadOnly(t *testing.T) {
	stack := newTestStack(t)
	stack.subs.setEntitlement("free")

	userID := uuid.New()
	lockedApiary := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, lockedApiary)
	stack.apiary.lock(lockedApiary)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": lockedApiary.String(),
		"name":      "Squatter",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("create under a read-only apiary: status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "parent_resource_pro_locked" {
		t.Fatalf("error code = %q, want %q", errBody.Error.Code, "parent_resource_pro_locked")
	}
}

func TestHiveFlow_SubscriptionServiceUnreachable(t *testing.T) {
	stack := newTestStack(t)
	stack.subServer.Close() // simulate subscription-service down

	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID.String(),
		"name":      "Should fail closed",
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("create with subscription down: status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
}

func TestHiveFlow_MediaLimit(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	photos := make([]string, 5)
	for i := range photos {
		id := uuid.New()
		stack.media.own(id)
		photos[i] = id.String()
	}

	// 1. Creating with 5 photos succeeds
	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID.String(),
		"name":      "Hive 5 photos",
		"images":    photos,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create with 5 photos: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var created hivehttp.Response
	decodeJSON(t, resp, &created)
	if len(created.Images) != 5 {
		t.Fatalf("created images count = %d, want 5", len(created.Images))
	}

	// 2. Creating with 6 photos fails with media_limit_reached
	photo6 := uuid.New()
	stack.media.own(photo6)
	tooMany := append(photos, photo6.String())

	resp = stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]any{
		"apiary_id": apiaryID.String(),
		"name":      "Hive 6 photos",
		"images":    tooMany,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with 6 photos: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "media_limit_reached" {
		t.Fatalf("error code = %q, want %q", errBody.Error.Code, "media_limit_reached")
	}

	// 3. Updating with 5 photos succeeds
	newPhotos := make([]string, 5)
	for i := range newPhotos {
		id := uuid.New()
		stack.media.own(id)
		newPhotos[i] = id.String()
	}
	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+created.ID.String(), token, map[string]any{
		"name":   "Hive replaced 5 photos",
		"images": newPhotos,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update with 5 photos: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// 4. Updating with 6 photos fails with media_limit_reached
	tooManyUpdate := append(newPhotos, photo6.String())
	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+created.ID.String(), token, map[string]any{
		"name":   "Hive 6 photos update",
		"images": tooManyUpdate,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update with 6 photos: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	decodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "media_limit_reached" {
		t.Fatalf("update error code = %q, want %q", errBody.Error.Code, "media_limit_reached")
	}

	// Verify that the existing 5 photos remain attached after the failed update attempt
	getResp := stack.request(t, http.MethodGet, "/api/v1/hives/"+created.ID.String(), token, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get after failed update: status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}
	var afterFailed hivehttp.Response
	decodeJSON(t, getResp, &afterFailed)
	if len(afterFailed.Images) != 5 {
		t.Fatalf("images after failed update = %d, want 5", len(afterFailed.Images))
	}

	// 5. Updating without touching images retains existing 5 photos and succeeds
	resp = stack.request(t, http.MethodPut, "/api/v1/hives/"+created.ID.String(), token, map[string]any{
		"name": "Renamed Hive without changing photos",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update with untouched images: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var updated hivehttp.Response
	decodeJSON(t, resp, &updated)
	if len(updated.Images) != 5 {
		t.Fatalf("images after update = %d, want 5", len(updated.Images))
	}
}

func TestHiveFlow_NeedsInspectionFilter(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	create := func(name string) hivehttp.Response {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiaryID.String(),
			"name":      name,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want %d", name, resp.StatusCode, http.StatusCreated)
		}
		var h hivehttp.Response
		decodeJSON(t, resp, &h)
		return h
	}

	recent := create("Recently inspected")
	stale := create("Stale")
	create("Never inspected") // deliberately not registered below

	now := time.Now().UTC()
	stack.inspections.setLatest(recent.ID, now.AddDate(0, 0, -1))
	stack.inspections.setLatest(stale.ID, now.AddDate(0, 0, -20))

	resp := stack.request(t, http.MethodGet, "/api/v1/hives?needs_inspection=true", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list needs_inspection: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var page pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &page)
	if page.Pagination.Total != 2 {
		t.Fatalf("total = %d, want 2 (stale + never inspected)", page.Pagination.Total)
	}
	for _, h := range page.Items {
		if h.ID == recent.ID {
			t.Error("recently inspected hive should not appear in needs_inspection filter")
		}
	}
}

func TestHiveFlow_NeedsInspectionFilter_FalseOrAbsentReturnsEverything(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryID)

	resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
		"apiary_id": apiaryID.String(),
		"name":      "H1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	for _, path := range []string{"/api/v1/hives", "/api/v1/hives?needs_inspection=false", "/api/v1/hives?needs_inspection=garbage"} {
		resp := stack.request(t, http.MethodGet, path, token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
		var page pagination.Response[hivehttp.Response]
		decodeJSON(t, resp, &page)
		if page.Pagination.Total != 1 {
			t.Errorf("GET %s: total = %d, want 1 (unfiltered)", path, page.Pagination.Total)
		}
	}
}

// TestHiveFlow_ListByApiary_NeedsInspectionFilter proves the apiary-scoped
// list combines its apiary filter with needs_inspection correctly (not just
// each filter in isolation): a stale hive in a different apiary must not
// leak into this apiary's filtered results.
func TestHiveFlow_ListByApiary_NeedsInspectionFilter(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryA := uuid.New()
	apiaryB := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryA)
	stack.apiary.allow(token, apiaryB)

	create := func(apiaryID uuid.UUID, name string) hivehttp.Response {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiaryID.String(),
			"name":      name,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want %d", name, resp.StatusCode, http.StatusCreated)
		}
		var h hivehttp.Response
		decodeJSON(t, resp, &h)
		return h
	}

	staleInA := create(apiaryA, "Stale in A")
	recentInA := create(apiaryA, "Recent in A")
	staleInB := create(apiaryB, "Stale in B")

	now := time.Now().UTC()
	stack.inspections.setLatest(staleInA.ID, now.AddDate(0, 0, -20))
	stack.inspections.setLatest(recentInA.ID, now.AddDate(0, 0, -1))
	stack.inspections.setLatest(staleInB.ID, now.AddDate(0, 0, -20))

	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries/"+apiaryA.String()+"/hives?needs_inspection=true", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var page pagination.Response[hivehttp.Response]
	decodeJSON(t, resp, &page)
	if page.Pagination.Total != 1 {
		t.Fatalf("total = %d, want 1 (only staleInA - staleInB belongs to a different apiary)", page.Pagination.Total)
	}
	if page.Items[0].ID != staleInA.ID {
		t.Errorf("got hive %s, want %s", page.Items[0].ID, staleInA.ID)
	}
}

func TestHiveFlow_ApiaryIDsWithHives(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	apiaryA := uuid.New()
	apiaryB := uuid.New()
	apiaryEmpty := uuid.New()
	token := stack.tokenFor(t, userID)
	stack.apiary.allow(token, apiaryA)
	stack.apiary.allow(token, apiaryB)
	stack.apiary.allow(token, apiaryEmpty)

	for _, apiaryID := range []uuid.UUID{apiaryA, apiaryA, apiaryB} {
		resp := stack.request(t, http.MethodPost, "/api/v1/hives", token, map[string]string{
			"apiary_id": apiaryID.String(),
			"name":      "H-" + uuid.New().String(),
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create hive under %s: status = %d, want %d", apiaryID, resp.StatusCode, http.StatusCreated)
		}
	}

	resp := stack.request(t, http.MethodGet, "/api/v1/hives/apiary-ids-with-hives", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apiary-ids-with-hives: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body struct {
		ApiaryIDs []uuid.UUID `json:"apiary_ids"`
	}
	decodeJSON(t, resp, &body)

	if len(body.ApiaryIDs) != 2 {
		t.Fatalf("apiary_ids = %v, want 2 entries (apiaryA once, apiaryB once)", body.ApiaryIDs)
	}
	got := map[uuid.UUID]bool{}
	for _, id := range body.ApiaryIDs {
		got[id] = true
	}
	if !got[apiaryA] || !got[apiaryB] {
		t.Errorf("apiary_ids = %v, want apiaryA and apiaryB", body.ApiaryIDs)
	}
	if got[apiaryEmpty] {
		t.Error("apiaryEmpty (no hives) should not appear in apiary-ids-with-hives")
	}
}

func TestHiveFlow_ApiaryIDsWithHives_WithoutTokenIsUnauthorized(t *testing.T) {
	stack := newTestStack(t)

	resp := stack.request(t, http.MethodGet, "/api/v1/hives/apiary-ids-with-hives", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}
