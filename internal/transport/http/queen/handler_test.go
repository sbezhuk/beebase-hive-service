package queen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	httpmw "github.com/sbezhuk/beebase-common/authmw"
	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	appqueen "github.com/sbezhuk/beebase-hive-service/internal/application/queen"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
	queenhttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/queen"
)

type fakeHiveRepo struct {
	hives map[uuid.UUID]*domainhive.Hive
}

func (r *fakeHiveRepo) GetByID(ctx context.Context, userID, hiveID uuid.UUID) (*domainhive.Hive, error) {
	h, ok := r.hives[hiveID]
	if !ok || h.UserID != userID || h.DeletedAt != nil {
		return nil, domainhive.ErrNotFound
	}
	return h, nil
}

func (r *fakeHiveRepo) WritableIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, h := range r.hives {
		if h.UserID == userID && h.DeletedAt == nil {
			ids = append(ids, h.ID)
		}
	}
	return ids, nil
}

type fakeQueenRepo struct {
	queens map[uuid.UUID]*domainqueen.Queen
}

func newFakeQueenRepo() *fakeQueenRepo {
	return &fakeQueenRepo{queens: make(map[uuid.UUID]*domainqueen.Queen)}
}

func (r *fakeQueenRepo) getOrdered(hiveID uuid.UUID) []*domainqueen.Queen {
	var list []*domainqueen.Queen
	for _, q := range r.queens {
		if q.HiveID == hiveID {
			list = append(list, q)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].IntroducedAt.Before(list[j].IntroducedAt)
	})
	return list
}

func (r *fakeQueenRepo) InsertInChain(ctx context.Context, q *domainqueen.Queen) error {
	existing := r.getOrdered(q.HiveID)
	for _, ex := range existing {
		if ex.IntroducedAt.Equal(q.IntroducedAt) {
			return domainqueen.ErrDuplicateIntroducedAt
		}
	}

	n := len(existing)
	if n == 0 {
		q.RemovedAt = nil
	} else if q.IntroducedAt.After(existing[n-1].IntroducedAt) {
		prev := existing[n-1]
		intro := q.IntroducedAt
		prev.RemovedAt = &intro
		q.RemovedAt = nil
	} else if q.IntroducedAt.Before(existing[0].IntroducedAt) {
		succIntro := existing[0].IntroducedAt
		q.RemovedAt = &succIntro
	} else {
		var prev, next *domainqueen.Queen
		for i := 0; i < n-1; i++ {
			if existing[i].IntroducedAt.Before(q.IntroducedAt) && existing[i+1].IntroducedAt.After(q.IntroducedAt) {
				prev = existing[i]
				next = existing[i+1]
				break
			}
		}
		if prev == nil || next == nil {
			return errors.New("failed to find interval")
		}
		intro := q.IntroducedAt
		prev.RemovedAt = &intro
		succIntro := next.IntroducedAt
		q.RemovedAt = &succIntro
	}

	r.queens[q.ID] = q
	return nil
}

func (r *fakeQueenRepo) GetCurrentByHiveID(ctx context.Context, hiveID uuid.UUID) (*domainqueen.Queen, error) {
	for _, q := range r.queens {
		if q.HiveID == hiveID && q.RemovedAt == nil {
			return q, nil
		}
	}
	return nil, domainqueen.ErrNotFound
}

func (r *fakeQueenRepo) GetByID(ctx context.Context, hiveID, queenID uuid.UUID) (*domainqueen.Queen, error) {
	q, ok := r.queens[queenID]
	if !ok || q.HiveID != hiveID {
		return nil, domainqueen.ErrNotFound
	}
	return q, nil
}

func (r *fakeQueenRepo) ListHistoryByHiveID(ctx context.Context, hiveID uuid.UUID) ([]*domainqueen.Queen, error) {
	list := r.getOrdered(hiveID)
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

func (r *fakeQueenRepo) UpdateInChain(ctx context.Context, hiveID, queenID uuid.UUID, year int, markedAt *time.Time, introducedAt time.Time, notes string) (*domainqueen.Queen, error) {
	existing := r.getOrdered(hiveID)
	targetIdx := -1
	for i, q := range existing {
		if q.ID == queenID {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		return nil, domainqueen.ErrNotFound
	}

	if targetIdx > 0 {
		prev := existing[targetIdx-1]
		if !introducedAt.After(prev.IntroducedAt) {
			return nil, domainqueen.ErrTimelineInvalid
		}
	}
	if targetIdx < len(existing)-1 {
		next := existing[targetIdx+1]
		if !introducedAt.Before(next.IntroducedAt) {
			return nil, domainqueen.ErrTimelineInvalid
		}
	}

	if targetIdx > 0 && !existing[targetIdx].IntroducedAt.Equal(introducedAt) {
		prev := existing[targetIdx-1]
		intro := introducedAt
		prev.RemovedAt = &intro
	}

	target := existing[targetIdx]
	target.Year = year
	target.MarkedAt = markedAt
	target.IntroducedAt = introducedAt
	target.Notes = notes
	target.UpdatedAt = time.Now().UTC()

	return target, nil
}

func (r *fakeQueenRepo) DeleteLatest(ctx context.Context, hiveID, queenID uuid.UUID) error {
	existing := r.getOrdered(hiveID)
	if len(existing) == 0 {
		return domainqueen.ErrNotFound
	}

	found := false
	for _, q := range existing {
		if q.ID == queenID {
			found = true
			break
		}
	}
	if !found {
		return domainqueen.ErrNotFound
	}

	latest := existing[len(existing)-1]
	if latest.ID != queenID {
		return domainqueen.ErrQueenNotLatest
	}

	delete(r.queens, queenID)

	if len(existing) > 1 {
		prev := existing[len(existing)-2]
		prev.RemovedAt = nil
	}

	return nil
}

type fakeApiaryVerifier struct{}

func (v *fakeApiaryVerifier) Verify(ctx context.Context, accessToken string, apiaryID uuid.UUID) (bool, error) {
	return true, nil
}

type fakeEntitlementResolver struct{}

func (e *fakeEntitlementResolver) GetEntitlement(ctx context.Context, accessToken string) (string, error) {
	return apphive.EntitlementPro, nil
}

type fakeTokenParser struct {
	userID uuid.UUID
}

func (p *fakeTokenParser) Parse(ctx context.Context, token string) (uuid.UUID, error) {
	return p.userID, nil
}

func setupTestRouter(userID uuid.UUID, h *domainhive.Hive) (http.Handler, *fakeQueenRepo) {
	hiveRepo := &fakeHiveRepo{hives: map[uuid.UUID]*domainhive.Hive{h.ID: h}}
	queenRepo := newFakeQueenRepo()
	svc := appqueen.NewService(queenRepo, hiveRepo, &fakeApiaryVerifier{}, &fakeEntitlementResolver{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := queenhttp.NewHandler(svc, logger)

	r := chi.NewRouter()
	r.Use(httpmw.RequireAuth(&fakeTokenParser{userID: userID}))

	r.Route("/api/v1/hives/{hiveId}", func(r chi.Router) {
		r.Get("/queen", handler.GetCurrent)
		r.Get("/queens", handler.ListHistory)
		r.Post("/queens", handler.Create)
		r.Get("/queens/{queenId}", handler.GetByID)
		r.Put("/queens/{queenId}", handler.Update)
		r.Delete("/queens/{queenId}", handler.Delete)
	})

	return r, queenRepo
}

func doRequest(router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer test-token")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestQueenHTTP_EndToEndChain(t *testing.T) {
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()

	h := domainhive.New(userID, apiaryID, "Hive A", "")
	h.ID = hiveID

	router, queenRepo := setupTestRouter(userID, h)

	// 1. GET /queen -> 404 queen_not_found initially
	rec := doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queen", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	// 2. POST /queens -> creates 2025 queen (Blue)
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2025,
		"introducedAt": "2025-01-01T00:00:00Z",
		"notes":        "First queen",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp queenhttp.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Year != 2025 || resp.MarkingColor != "blue" || resp.MarkingColorHex != "#A7C7F7" {
		t.Fatalf("unexpected color mapping in response: %+v", resp)
	}
	queen1ID := resp.ID

	// 3. GET /queen -> returns 2025 queen
	rec = doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queen", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 4. POST /queens with 2026 queen -> automatically closes previous and becomes current
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2026,
		"introducedAt": "2026-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	queen2ID := resp.ID

	// 5. POST /queens with 2027 queen
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2027,
		"introducedAt": "2027-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	queen3ID := resp.ID

	// 6. GET /queens -> returns all 3 queens (newest first)
	rec = doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queens", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var history []queenhttp.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 queens in history, got %d", len(history))
	}
	if history[0].ID != queen3ID || history[1].ID != queen2ID || history[2].ID != queen1ID {
		t.Fatalf("unexpected history order")
	}

	// 7. DELETE middle queen (queen2) -> 409 Conflict with queen_not_latest
	rec = doRequest(router, http.MethodDelete, "/api/v1/hives/"+hiveID.String()+"/queens/"+queen2ID.String(), nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when deleting middle queen, got %d", rec.Code)
	}

	// 8. DELETE latest queen (queen3) -> 204 No Content
	rec = doRequest(router, http.MethodDelete, "/api/v1/hives/"+hiveID.String()+"/queens/"+queen3ID.String(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 when deleting latest queen, got %d", rec.Code)
	}

	// 9. GET /queen -> queen2 is now current!
	rec = doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queen", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for current queen rollback, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.ID != queen2ID {
		t.Fatalf("expected queen2 to be rolled back to current, got %v", resp.ID)
	}

	// 10. PUT /queens/{queen2ID} within bounds -> succeeds
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+queen2ID.String(), map[string]any{
		"year":         2026,
		"introducedAt": "2026-06-01T00:00:00Z",
		"notes":        "Updated queen2",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid update, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify queen1's removed_at was updated to 2026-06-01
	q1 := queenRepo.queens[queen1ID]
	if q1.RemovedAt == nil || q1.RemovedAt.Format(time.RFC3339) != "2026-06-01T00:00:00Z" {
		t.Fatalf("expected queen1 removed_at to be updated, got: %v", q1.RemovedAt)
	}
}

func TestQueenHTTP_Validation(t *testing.T) {
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()

	h := domainhive.New(userID, apiaryID, "Hive B", "")
	h.ID = hiveID

	router, _ := setupTestRouter(userID, h)

	// Year missing (0)
	rec := doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"introducedAt": "2026-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing year, got %d", rec.Code)
	}

	// Year invalid (e.g. 500)
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         500,
		"introducedAt": "2026-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid year, got %d", rec.Code)
	}

	// Missing introducedAt
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year": 2026,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing introducedAt, got %d", rec.Code)
	}
	var valErr struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &valErr); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if valErr.Error.Fields["introducedAt"] != "introduced_at_required" {
		t.Fatalf("expected fields.introducedAt = introduced_at_required, got: %v", valErr.Error.Fields)
	}
}

func TestQueenHTTP_DuplicateIntroducedAt_Conflict(t *testing.T) {
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()

	h := domainhive.New(userID, apiaryID, "Hive C", "")
	h.ID = hiveID

	router, _ := setupTestRouter(userID, h)

	doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2025,
		"introducedAt": "2025-01-01T00:00:00Z",
	})

	rec := doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2026,
		"introducedAt": "2025-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate introducedAt, got %d", rec.Code)
	}
}

func TestQueenHTTP_UpdateIntroducedAt_StrictBounds(t *testing.T) {
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()

	h := domainhive.New(userID, apiaryID, "Hive Chain", "")
	h.ID = hiveID

	router, _ := setupTestRouter(userID, h)

	// Create Q1 (2025-01-01)
	rec1 := doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2025,
		"introducedAt": "2025-01-01T00:00:00Z",
	})
	var r1 queenhttp.Response
	_ = json.Unmarshal(rec1.Body.Bytes(), &r1)
	q1ID := r1.ID

	// Create Q2 (2026-01-01)
	rec2 := doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2026,
		"introducedAt": "2026-01-01T00:00:00Z",
	})
	var r2 queenhttp.Response
	_ = json.Unmarshal(rec2.Body.Bytes(), &r2)
	q2ID := r2.ID

	// Create Q3 (2027-01-01)
	rec3 := doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2027,
		"introducedAt": "2027-01-01T00:00:00Z",
	})
	var r3 queenhttp.Response
	_ = json.Unmarshal(rec3.Body.Bytes(), &r3)
	q3ID := r3.ID

	// 1. Q2 equal to Q1 (exact equality with previous) -> 400 timeline_invalid
	rec := doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+q2ID.String(), map[string]any{
		"year":         2026,
		"introducedAt": "2025-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for equal to previous, got %d", rec.Code)
	}
	var valErr struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &valErr)
	if valErr.Error.Fields["introducedAt"] != "timeline_invalid" {
		t.Errorf("expected timeline_invalid, got: %v", valErr.Error.Fields)
	}

	// 2. Q2 equal to Q3 (exact equality with next) -> 400 timeline_invalid
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+q2ID.String(), map[string]any{
		"year":         2026,
		"introducedAt": "2027-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for equal to next, got %d", rec.Code)
	}

	// 3. Q1 (oldest) equal to Q2 -> 400 timeline_invalid
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+q1ID.String(), map[string]any{
		"year":         2025,
		"introducedAt": "2026-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oldest equal to next, got %d", rec.Code)
	}

	// 4. Q3 (latest) equal to Q2 -> 400 timeline_invalid
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+q3ID.String(), map[string]any{
		"year":         2027,
		"introducedAt": "2026-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for latest equal to previous, got %d", rec.Code)
	}

	// 5. Valid Q2 update: 2026-04-01 -> 200 OK
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+q2ID.String(), map[string]any{
		"year":         2026,
		"introducedAt": "2026-04-01T00:00:00Z",
		"notes":        "April Q2",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid update, got %d", rec.Code)
	}

	// 6. Verify Q1's removed_at via GET /queens/{q1ID} is now 2026-04-01
	rec = doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queens/"+q1ID.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for get Q1, got %d", rec.Code)
	}
	var getQ1 queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &getQ1)
	if getQ1.RemovedAt == nil || getQ1.RemovedAt.Format(time.RFC3339) != "2026-04-01T00:00:00Z" {
		t.Fatalf("Q1 removedAt not recalculated: %v", getQ1.RemovedAt)
	}
}

