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
	hivehttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/hive"
	"github.com/sbezhuk/beebase-common/pagination"
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

func (r *fakeHiveRepo) Create(ctx context.Context, h *domainhive.Hive) error {
	r.hives[h.ID] = h
	return nil
}
func (r *fakeHiveRepo) CreateWithLimit(ctx context.Context, h *domainhive.Hive, maxCount int) error {
	r.hives[h.ID] = h
	return nil
}
func (r *fakeHiveRepo) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	return len(r.hives), nil
}
func (r *fakeHiveRepo) ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspectionOnly bool, needsInspectionHiveIDs []uuid.UUID) ([]*domainhive.Hive, int, error) {
	return nil, 0, nil
}
func (r *fakeHiveRepo) ListByApiary(ctx context.Context, userID, apiaryID uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspectionOnly bool, needsInspectionHiveIDs []uuid.UUID) ([]*domainhive.Hive, int, error) {
	return nil, 0, nil
}
func (r *fakeHiveRepo) Update(ctx context.Context, h *domainhive.Hive) error {
	r.hives[h.ID] = h
	return nil
}
func (r *fakeHiveRepo) ListIDsByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (r *fakeHiveRepo) DistinctApiaryIDsWithHives(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (r *fakeHiveRepo) ListAllByApiary(ctx context.Context, userID, apiaryID uuid.UUID) ([]*domainhive.Hive, error) {
	return nil, nil
}
func (r *fakeHiveRepo) HardDelete(ctx context.Context, userID, hiveID uuid.UUID) error {
	delete(r.hives, hiveID)
	return nil
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

func (r *fakeQueenRepo) withIncomingReason(q *domainqueen.Queen) *domainqueen.Queen {
	existing := r.getOrdered(q.HiveID)
	var incomingReason *domainqueen.ReplacementReason
	for i, item := range existing {
		if item.ID == q.ID {
			if i > 0 {
				incomingReason = existing[i-1].ReplacementReason
			}
			break
		}
	}
	clone := *q
	clone.ReplacementReason = incomingReason
	return &clone
}

func (r *fakeQueenRepo) InsertInChain(ctx context.Context, q *domainqueen.Queen, predecessorReason *domainqueen.ReplacementReason) error {
	existing := r.getOrdered(q.HiveID)
	for _, ex := range existing {
		if ex.IntroducedAt.Equal(q.IntroducedAt) {
			return domainqueen.ErrDuplicateIntroducedAt
		}
	}

	n := len(existing)
	if n == 0 {
		if predecessorReason != nil {
			return domainqueen.ErrReplacementReasonNotAllowed
		}
		q.RemovedAt = nil
		q.ReplacementReason = nil
	} else if q.IntroducedAt.After(existing[n-1].IntroducedAt) {
		prev := existing[n-1]
		intro := q.IntroducedAt
		prev.RemovedAt = &intro
		prev.ReplacementReason = predecessorReason
		q.RemovedAt = nil
		q.ReplacementReason = predecessorReason
	} else if q.IntroducedAt.Before(existing[0].IntroducedAt) {
		if predecessorReason != nil {
			return domainqueen.ErrReplacementReasonNotAllowed
		}
		succIntro := existing[0].IntroducedAt
		q.RemovedAt = &succIntro
		q.ReplacementReason = nil
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
		prev.ReplacementReason = predecessorReason
		succIntro := next.IntroducedAt
		q.RemovedAt = &succIntro
		q.ReplacementReason = predecessorReason
	}

	stored := *q
	stored.ReplacementReason = nil
	r.queens[q.ID] = &stored
	return nil
}

func (r *fakeQueenRepo) GetCurrentByHiveID(ctx context.Context, hiveID uuid.UUID) (*domainqueen.Queen, error) {
	for _, q := range r.queens {
		if q.HiveID == hiveID && q.RemovedAt == nil {
			return r.withIncomingReason(q), nil
		}
	}
	return nil, domainqueen.ErrNotFound
}

func (r *fakeQueenRepo) GetByID(ctx context.Context, hiveID, queenID uuid.UUID) (*domainqueen.Queen, error) {
	q, ok := r.queens[queenID]
	if !ok || q.HiveID != hiveID {
		return nil, domainqueen.ErrNotFound
	}
	return r.withIncomingReason(q), nil
}

func (r *fakeQueenRepo) ListHistoryByHiveID(ctx context.Context, hiveID uuid.UUID) ([]*domainqueen.Queen, error) {
	list := r.getOrdered(hiveID)
	res := make([]*domainqueen.Queen, len(list))
	for i := range list {
		res[i] = r.withIncomingReason(list[i])
	}
	for i, j := 0, len(res)-1; i < j; i, j = i+1, j-1 {
		res[i], res[j] = res[j], res[i]
	}
	return res, nil
}

func (r *fakeQueenRepo) UpdateInChain(ctx context.Context, hiveID, queenID uuid.UUID, year int, markedAt *time.Time, introducedAt time.Time, replacementReason *domainqueen.ReplacementReason, hasReplacementReason bool, notes string) (*domainqueen.Queen, error) {
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

	if targetIdx == 0 && hasReplacementReason && replacementReason != nil {
		return nil, domainqueen.ErrReplacementReasonNotAllowed
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

	if targetIdx > 0 {
		prev := existing[targetIdx-1]
		if !existing[targetIdx].IntroducedAt.Equal(introducedAt) {
			intro := introducedAt
			prev.RemovedAt = &intro
		}
		if hasReplacementReason {
			prev.ReplacementReason = replacementReason
		}
	}

	target := existing[targetIdx]
	target.Year = year
	target.MarkedAt = markedAt
	target.IntroducedAt = introducedAt
	target.Notes = notes
	target.UpdatedAt = time.Now().UTC()

	return r.withIncomingReason(target), nil
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
		prev.ReplacementReason = nil
	}

	return nil
}

type fakeApiaryVerifier struct{}

func (v *fakeApiaryVerifier) Verify(ctx context.Context, accessToken string, apiaryID uuid.UUID) (bool, error) {
	return true, nil
}

func (v *fakeApiaryVerifier) WritableApiaryID(ctx context.Context, accessToken string) (*uuid.UUID, bool, error) {
	return nil, true, nil
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

	hiveSvc := apphive.NewService(hiveRepo, &fakeApiaryVerifier{}, nil, nil, nil, &fakeEntitlementResolver{}, queenRepo)
	hiveHandler := hivehttp.NewHandler(hiveSvc, logger, "https://api.beebase.app")

	r := chi.NewRouter()
	r.Use(httpmw.RequireAuth(&fakeTokenParser{userID: userID}))

	r.Route("/api/v1/hives/{hiveId}", func(r chi.Router) {
		r.Get("/", hiveHandler.Get)
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

func TestQueenHTTP_ReplacementReason(t *testing.T) {
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()
	h := &domainhive.Hive{
		ID:        hiveID,
		UserID:    userID,
		ApiaryID:  apiaryID,
		Name:      "Test Hive",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	router, _ := setupTestRouter(userID, h)

	// 1. POST first queen with replacementReason -> 400 replacement_reason_not_allowed
	rec := doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":              2025,
		"introducedAt":      "2025-04-10T00:00:00Z",
		"replacementReason": "LOW_EGG_LAYING",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for first queen with reason, got %d: %s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	if errResp.Error.Fields["replacementReason"] != queenhttp.CodeReplacementReasonNotAllowed {
		t.Errorf("expected %q, got %q", queenhttp.CodeReplacementReasonNotAllowed, errResp.Error.Fields["replacementReason"])
	}

	// 2. POST with invalid enum -> 400 replacement_reason_invalid
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":              2025,
		"introducedAt":      "2025-04-10T00:00:00Z",
		"replacementReason": "INVALID_ENUM_CODE",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid enum, got %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	if errResp.Error.Fields["replacementReason"] != queenhttp.CodeReplacementReasonInvalid {
		t.Errorf("expected %q, got %q", queenhttp.CodeReplacementReasonInvalid, errResp.Error.Fields["replacementReason"])
	}

	// 3. POST first queen without reason -> 201, replacementReason == null
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":         2025,
		"introducedAt": "2025-04-10T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var qA queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &qA)
	if qA.ReplacementReason != nil {
		t.Errorf("first queen replacementReason should be nil, got %v", *qA.ReplacementReason)
	}

	// 4. POST second queen with replacementReason: "LOW_EGG_LAYING" -> 201, qB has replacementReason == "LOW_EGG_LAYING"
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":              2026,
		"introducedAt":      "2026-05-15T00:00:00Z",
		"replacementReason": "LOW_EGG_LAYING",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var qB queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &qB)
	if qB.ReplacementReason == nil || *qB.ReplacementReason != "LOW_EGG_LAYING" {
		t.Fatalf("new current queen replacementReason should be LOW_EGG_LAYING, got %v", qB.ReplacementReason)
	}

	// Predecessor qA has incoming transition reason = nil
	rec = doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queens/"+qA.ID.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var qAGet queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &qAGet)
	if qAGet.ReplacementReason != nil {
		t.Fatalf("expected oldest queen qA to have replacementReason nil, got %v", qAGet.ReplacementReason)
	}

	// 5. PUT oldest queen qA with replacementReason -> 400 replacement_reason_not_allowed
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+qA.ID.String(), map[string]any{
		"year":              2025,
		"introducedAt":      "2025-04-10T00:00:00Z",
		"replacementReason": "AGING_AND_WEAR",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oldest queen with reason, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	if errResp.Error.Fields["replacementReason"] != queenhttp.CodeReplacementReasonNotAllowed {
		t.Errorf("expected %q, got %q", queenhttp.CodeReplacementReasonNotAllowed, errResp.Error.Fields["replacementReason"])
	}

	// 6. PUT current queen qB with replacementReason: "DISEASE_OR_POOR_QUALITY" -> 200 OK
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+qB.ID.String(), map[string]any{
		"year":              2026,
		"introducedAt":      "2026-05-15T00:00:00Z",
		"replacementReason": "DISEASE_OR_POOR_QUALITY",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for editing current queen reason, got %d: %s", rec.Code, rec.Body.String())
	}
	var qBUpdated queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &qBUpdated)
	if qBUpdated.ReplacementReason == nil || *qBUpdated.ReplacementReason != "DISEASE_OR_POOR_QUALITY" {
		t.Errorf("expected DISEASE_OR_POOR_QUALITY, got %v", qBUpdated.ReplacementReason)
	}

	// 7. PUT qB with omitted replacementReason -> 200 OK, preserves existing reason
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+qB.ID.String(), map[string]any{
		"year":         2026,
		"introducedAt": "2026-05-15T00:00:00Z",
		"notes":        "Updated notes",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for omitted reason update, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &qBUpdated)
	if qBUpdated.ReplacementReason == nil || *qBUpdated.ReplacementReason != "DISEASE_OR_POOR_QUALITY" {
		t.Errorf("expected DISEASE_OR_POOR_QUALITY preserved, got %v", qBUpdated.ReplacementReason)
	}

	// 8. PUT qB with replacementReason: null -> 200 OK, clears reason
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+qB.ID.String(), map[string]any{
		"year":              2026,
		"introducedAt":      "2026-05-15T00:00:00Z",
		"replacementReason": nil,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for clearing reason, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &qBUpdated)
	if qBUpdated.ReplacementReason != nil {
		t.Errorf("expected cleared reason nil, got %v", *qBUpdated.ReplacementReason)
	}

	// 9. Introduce qC with reason on predecessor qB -> 201
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":              2027,
		"introducedAt":      "2027-04-20T00:00:00Z",
		"replacementReason": "NATURAL_SUPERSEDURE",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	var qC queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &qC)
	if qC.ReplacementReason == nil || *qC.ReplacementReason != "NATURAL_SUPERSEDURE" {
		t.Fatalf("expected qC to have NATURAL_SUPERSEDURE, got %v", qC.ReplacementReason)
	}

	// Update qB (historical queen) with incoming reason "INJURY_OR_MUTILATION"
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+qB.ID.String(), map[string]any{
		"year":              2026,
		"introducedAt":      "2026-05-15T00:00:00Z",
		"replacementReason": "INJURY_OR_MUTILATION",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for editing qB reason, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &qBUpdated)
	if qBUpdated.ReplacementReason == nil || *qBUpdated.ReplacementReason != "INJURY_OR_MUTILATION" {
		t.Errorf("expected qB to have INJURY_OR_MUTILATION, got %v", qBUpdated.ReplacementReason)
	}

	// DELETE qC: destroys transition B -> C, qB becomes active, but A -> B is preserved!
	rec = doRequest(router, http.MethodDelete, "/api/v1/hives/"+hiveID.String()+"/queens/"+qC.ID.String(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	// GET qB - must now have removedAt = null, and replacementReason = "INJURY_OR_MUTILATION" preserved
	rec = doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queens/"+qB.ID.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var qBRolledBack queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &qBRolledBack)
	if qBRolledBack.RemovedAt != nil {
		t.Errorf("expected rolled back queen to have removedAt null, got %v", qBRolledBack.RemovedAt)
	}
	if qBRolledBack.ReplacementReason == nil || *qBRolledBack.ReplacementReason != "INJURY_OR_MUTILATION" {
		t.Errorf("expected rolled back queen to preserve incoming reason INJURY_OR_MUTILATION, got %v", qBRolledBack.ReplacementReason)
	}
}

func TestQueenHTTP_ColorAndBackendControlledFieldsAndEmbedding(t *testing.T) {
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()

	h := &domainhive.Hive{
		ID:        hiveID,
		UserID:    userID,
		ApiaryID:  apiaryID,
		Name:      "Hive Color Test",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	router, _ := setupTestRouter(userID, h)

	// 1. GET /api/v1/hives/{hiveId} initially has currentQueen == nil
	rec := doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for get hive, got %d: %s", rec.Code, rec.Body.String())
	}
	var hiveResp struct {
		ID           uuid.UUID           `json:"id"`
		CurrentQueen *queenhttp.Response `json:"currentQueen"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &hiveResp)
	if hiveResp.CurrentQueen != nil {
		t.Fatalf("expected currentQueen to be nil initially, got: %+v", hiveResp.CurrentQueen)
	}

	// 2. Client attempts to pass markingColor, markingColorHex, removedAt in POST
	rec = doRequest(router, http.MethodPost, "/api/v1/hives/"+hiveID.String()+"/queens", map[string]any{
		"year":            2026,
		"introducedAt":    "2026-05-01T00:00:00Z",
		"markingColor":    "red",                          // Backend-controlled; client override should be ignored
		"markingColorHex": "#FF0000",                      // Backend-controlled; client override should be ignored
		"removedAt":       "2026-08-01T00:00:00Z",          // Backend-controlled; client override should be ignored
		"notes":           "Queen 2026",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var createdQ queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &createdQ)
	if createdQ.MarkingColor != "white" || createdQ.MarkingColorHex != "#F8F9FA" {
		t.Errorf("marking color not authoritatively derived: got %s (%s), want white (#F8F9FA)", createdQ.MarkingColor, createdQ.MarkingColorHex)
	}
	if createdQ.RemovedAt != nil {
		t.Errorf("removedAt was not backend-controlled: got %v", createdQ.RemovedAt)
	}

	// 3. Embedded currentQueen in GET /hives/{hiveId} matches GET /hives/{hiveId}/queen
	recHive := doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String(), nil)
	recQueen := doRequest(router, http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/queen", nil)
	if recHive.Code != http.StatusOK || recQueen.Code != http.StatusOK {
		t.Fatalf("failed fetching hive or queen: %d, %d", recHive.Code, recQueen.Code)
	}
	var hiveWithQ struct {
		CurrentQueen *queenhttp.Response `json:"currentQueen"`
	}
	_ = json.Unmarshal(recHive.Body.Bytes(), &hiveWithQ)
	var dedicatedQ queenhttp.Response
	_ = json.Unmarshal(recQueen.Body.Bytes(), &dedicatedQ)

	if hiveWithQ.CurrentQueen == nil {
		t.Fatal("expected embedded currentQueen to be non-nil")
	}
	if *hiveWithQ.CurrentQueen != dedicatedQ {
		t.Fatalf("embedded currentQueen (%+v) does not match dedicated endpoint (%+v)", *hiveWithQ.CurrentQueen, dedicatedQ)
	}

	// 4. PUT year 2026 -> 2027 changes derived marking color to yellow (#FFE08A)
	rec = doRequest(router, http.MethodPut, "/api/v1/hives/"+hiveID.String()+"/queens/"+createdQ.ID.String(), map[string]any{
		"year":            2027,
		"introducedAt":    "2026-05-01T00:00:00Z",
		"markingColor":    "blue",
		"markingColorHex": "#0000FF",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for PUT year, got %d: %s", rec.Code, rec.Body.String())
	}
	var updatedQ queenhttp.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &updatedQ)
	if updatedQ.Year != 2027 || updatedQ.MarkingColor != "yellow" || updatedQ.MarkingColorHex != "#FFE08A" {
		t.Errorf("PUT year did not update derived color: got year=%d, color=%s, hex=%s", updatedQ.Year, updatedQ.MarkingColor, updatedQ.MarkingColorHex)
	}
}

