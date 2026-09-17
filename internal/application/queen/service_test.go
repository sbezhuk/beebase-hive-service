package queen_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	appqueen "github.com/sbezhuk/beebase-hive-service/internal/application/queen"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// fakeHiveRepo implements appqueen.HiveRepository in memory.
type fakeHiveRepo struct {
	hives       map[uuid.UUID]*domainhive.Hive
	writableIDs []uuid.UUID
}

func newFakeHiveRepo() *fakeHiveRepo {
	return &fakeHiveRepo{hives: make(map[uuid.UUID]*domainhive.Hive)}
}

func (r *fakeHiveRepo) GetByID(ctx context.Context, userID, hiveID uuid.UUID) (*domainhive.Hive, error) {
	h, ok := r.hives[hiveID]
	if !ok || h.UserID != userID || h.DeletedAt != nil {
		return nil, domainhive.ErrNotFound
	}
	return h, nil
}

func (r *fakeHiveRepo) WritableIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error) {
	if r.writableIDs != nil {
		return r.writableIDs, nil
	}
	var ids []uuid.UUID
	for _, h := range r.hives {
		if h.UserID == userID && h.DeletedAt == nil {
			ids = append(ids, h.ID)
			if limit > 0 && len(ids) >= limit {
				break
			}
		}
	}
	return ids, nil
}

// fakeQueenRepo implements domainqueen.Repository in memory with strict chain logic.
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
	// reverse to newest first
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

type fakeApiaryVerifier struct {
	writable bool
}

func (v *fakeApiaryVerifier) Verify(ctx context.Context, accessToken string, apiaryID uuid.UUID) (bool, error) {
	return v.writable, nil
}

type fakeSubscriptionClient struct {
	entitlement string
}

func (s *fakeSubscriptionClient) GetEntitlement(ctx context.Context, accessToken string) (string, error) {
	return s.entitlement, nil
}

func setupTest(t *testing.T) (*appqueen.Service, *fakeQueenRepo, *fakeHiveRepo, uuid.UUID, uuid.UUID) {
	t.Helper()
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()

	hives := newFakeHiveRepo()
	hives.hives[hiveID] = &domainhive.Hive{
		ID:        hiveID,
		UserID:    userID,
		ApiaryID:  apiaryID,
		Name:      "Hive 1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	queens := newFakeQueenRepo()
	apiaries := &fakeApiaryVerifier{writable: true}
	subs := &fakeSubscriptionClient{entitlement: "pro"}

	svc := appqueen.NewService(queens, hives, apiaries, subs)
	return svc, queens, hives, userID, hiveID
}

func TestService_CreateFirstQueen(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	intro := time.Date(2025, 4, 10, 0, 0, 0, 0, time.UTC)

	q, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		Year:         2025,
		IntroducedAt: intro,
		Notes:        "First queen",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !q.IsCurrent() {
		t.Errorf("first queen must be current (removed_at == nil)")
	}
	if !q.IntroducedAt.Equal(intro) {
		t.Errorf("got introduced_at %v, want %v", q.IntroducedAt, intro)
	}
	if q.MarkingColor() != domainqueen.ColorBlue {
		t.Errorf("got color %s, want blue", q.MarkingColor())
	}
}

func TestService_AppendNewerQueen_ClosesPrevious(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	introA := time.Date(2025, 4, 10, 0, 0, 0, 0, time.UTC)
	introB := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	qA, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		Year:         2025,
		IntroducedAt: introA,
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}

	qB, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		Year:         2026,
		IntroducedAt: introB,
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}

	// qB must be current
	if !qB.IsCurrent() {
		t.Errorf("qB must be current")
	}

	// qA must have removed_at == qB.introduced_at
	storedA, err := svc.GetByID(context.Background(), userID, hiveID, qA.ID)
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	if storedA.RemovedAt == nil || !storedA.RemovedAt.Equal(introB) {
		t.Errorf("qA removed_at = %v, want %v", storedA.RemovedAt, introB)
	}
}

func TestService_AppendMultiple_StrictChain(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB})
	qC, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC})

	history, err := svc.ListHistory(context.Background(), userID, hiveID)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("got %d history items, want 3", len(history))
	}
	// history ordered newest first: C, B, A
	if history[0].ID != qC.ID || history[1].ID != qB.ID || history[2].ID != qA.ID {
		t.Errorf("unexpected history ordering")
	}

	if history[0].RemovedAt != nil {
		t.Errorf("latest queen C must have removed_at == nil")
	}
	if history[1].RemovedAt == nil || !history[1].RemovedAt.Equal(tC) {
		t.Errorf("B removed_at must equal C introduced_at")
	}
	if history[2].RemovedAt == nil || !history[2].RemovedAt.Equal(tB) {
		t.Errorf("A removed_at must equal B introduced_at")
	}
}

func TestService_RetroactiveMiddleInsertion(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA})
	qC, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC})

	// Retroactively insert B between A and C
	qB, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB})
	if err != nil {
		t.Fatalf("retroactive insert B: %v", err)
	}

	// Verify B's boundaries
	if qB.RemovedAt == nil || !qB.RemovedAt.Equal(tC) {
		t.Errorf("qB removed_at = %v, want %v", qB.RemovedAt, tC)
	}

	// Verify A's boundary was adjusted to B
	storedA, _ := svc.GetByID(context.Background(), userID, hiveID, qA.ID)
	if storedA.RemovedAt == nil || !storedA.RemovedAt.Equal(tB) {
		t.Errorf("storedA removed_at = %v, want %v", storedA.RemovedAt, tB)
	}

	// Verify C remains current
	storedC, _ := svc.GetByID(context.Background(), userID, hiveID, qC.ID)
	if !storedC.IsCurrent() {
		t.Errorf("storedC must remain current")
	}
}

func TestService_InsertOldest(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC})

	// Insert A before B
	qA, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA})
	if err != nil {
		t.Fatalf("insert oldest A: %v", err)
	}

	if qA.RemovedAt == nil || !qA.RemovedAt.Equal(tB) {
		t.Errorf("qA removed_at = %v, want %v", qA.RemovedAt, tB)
	}
}

func TestService_DuplicateIntroducedAt_Rejected(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	intro := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: intro})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: intro})
	if !errors.Is(err, domainqueen.ErrDuplicateIntroducedAt) {
		t.Errorf("got %v, want ErrDuplicateIntroducedAt", err)
	}
}

func TestService_UpdateIntroducedAt_WithinBounds(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC})

	// Move B to 2026-06-01 (strictly between 2025-01-01 and 2027-01-01)
	newTB := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	updatedB, err := svc.Update(context.Background(), userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
		Year:         2026,
		IntroducedAt: newTB,
		Notes:        "Updated B",
	})
	if err != nil {
		t.Fatalf("update B: %v", err)
	}
	if !updatedB.IntroducedAt.Equal(newTB) {
		t.Errorf("updatedB introduced_at = %v, want %v", updatedB.IntroducedAt, newTB)
	}

	// Check that A's removed_at updated to newTB
	storedA, _ := svc.GetByID(context.Background(), userID, hiveID, qA.ID)
	if storedA.RemovedAt == nil || !storedA.RemovedAt.Equal(newTB) {
		t.Errorf("storedA removed_at = %v, want %v", storedA.RemovedAt, newTB)
	}
}

func TestService_UpdateIntroducedAt_ViolatingBounds_Rejected(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC})

	// Try moving B before A (2024-12-01)
	invalidBefore := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
	_, err := svc.Update(context.Background(), userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
		Year:         2026,
		IntroducedAt: invalidBefore,
	})
	if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
		t.Errorf("expected ErrTimelineInvalid when moving before previous, got: %v", err)
	}

	// Try moving B after C (2028-01-01)
	invalidAfter := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = svc.Update(context.Background(), userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
		Year:         2026,
		IntroducedAt: invalidAfter,
	})
	if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
		t.Errorf("expected ErrTimelineInvalid when moving after successor, got: %v", err)
	}
}

func TestService_DeleteLatest_RollbackPredecessor(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB})
	qC, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC})

	// Delete latest (C)
	err := svc.Delete(context.Background(), userID, "token", hiveID, qC.ID)
	if err != nil {
		t.Fatalf("delete C: %v", err)
	}

	// B must now be current (removed_at == nil)
	storedB, err := svc.GetByID(context.Background(), userID, hiveID, qB.ID)
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if !storedB.IsCurrent() {
		t.Errorf("storedB must now be current after C deletion")
	}

	// Delete B
	err = svc.Delete(context.Background(), userID, "token", hiveID, qB.ID)
	if err != nil {
		t.Fatalf("delete B: %v", err)
	}

	// A must now be current
	storedA, err := svc.GetByID(context.Background(), userID, hiveID, qA.ID)
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	if !storedA.IsCurrent() {
		t.Errorf("storedA must now be current after B deletion")
	}

	// Delete A
	err = svc.Delete(context.Background(), userID, "token", hiveID, qA.ID)
	if err != nil {
		t.Fatalf("delete A: %v", err)
	}

	// History must be empty, no current queen
	_, err = svc.GetCurrent(context.Background(), userID, hiveID)
	if !errors.Is(err, domainqueen.ErrNotFound) {
		t.Errorf("expected ErrNotFound for current queen, got %v", err)
	}
}

func TestService_DeleteNonLatest_FailsWithQueenNotLatest(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC})

	// Attempt to delete middle queen B
	err := svc.Delete(context.Background(), userID, "token", hiveID, qB.ID)
	if !errors.Is(err, domainqueen.ErrQueenNotLatest) {
		t.Errorf("expected ErrQueenNotLatest when deleting middle queen B, got: %v", err)
	}

	// Attempt to delete oldest queen A
	err = svc.Delete(context.Background(), userID, "token", hiveID, qA.ID)
	if !errors.Is(err, domainqueen.ErrQueenNotLatest) {
		t.Errorf("expected ErrQueenNotLatest when deleting oldest queen A, got: %v", err)
	}

	// Ensure B and A still exist
	if _, err := svc.GetByID(context.Background(), userID, hiveID, qB.ID); err != nil {
		t.Errorf("queen B should still exist")
	}
	if _, err := svc.GetByID(context.Background(), userID, hiveID, qA.ID); err != nil {
		t.Errorf("queen A should still exist")
	}
}

func TestService_FreeTierRestrictions(t *testing.T) {
	userID := uuid.New()
	apiaryID := uuid.New()
	hiveID := uuid.New()

	hives := newFakeHiveRepo()
	hives.hives[hiveID] = &domainhive.Hive{
		ID:        hiveID,
		UserID:    userID,
		ApiaryID:  apiaryID,
		Name:      "Hive 1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	queens := newFakeQueenRepo()
	apiaries := &fakeApiaryVerifier{writable: false} // apiary read-only!
	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementFree}

	svc := appqueen.NewService(queens, hives, apiaries, subs)
	intro := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		Year:         2026,
		IntroducedAt: intro,
	})
	if !errors.Is(err, appqueen.ErrParentReadOnly) {
		t.Errorf("expected ErrParentReadOnly, got %v", err)
	}
}

func TestService_IntroducedAt_StrictBoundsAudit_14Cases(t *testing.T) {
	ctx := context.Background()

	setupChain := func(t *testing.T) (*appqueen.Service, uuid.UUID, uuid.UUID, *domainqueen.Queen, *domainqueen.Queen, *domainqueen.Queen) {
		t.Helper()
		svc, _, _, userID, hiveID := setupTest(t)
		tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{Year: 2025, IntroducedAt: tA, Notes: "Queen A"})
		if err != nil {
			t.Fatalf("create A: %v", err)
		}
		qB, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{Year: 2026, IntroducedAt: tB, Notes: "Queen B"})
		if err != nil {
			t.Fatalf("create B: %v", err)
		}
		qC, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{Year: 2027, IntroducedAt: tC, Notes: "Queen C"})
		if err != nil {
			t.Fatalf("create C: %v", err)
		}
		return svc, userID, hiveID, qA, qB, qC
	}

	// Case 1 & 11: Middle queen moved within bounds -> succeeds and recalculates predecessor's removed_at
	t.Run("Case 1 & 11: Middle queen within bounds updates predecessor removed_at", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, qC := setupChain(t)
		newB := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

		updatedB, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: newB,
			Notes:        "Queen B moved",
		})
		if err != nil {
			t.Fatalf("update B within bounds failed: %v", err)
		}
		if !updatedB.IntroducedAt.Equal(newB) {
			t.Errorf("B introducedAt = %v, want %v", updatedB.IntroducedAt, newB)
		}
		if updatedB.RemovedAt == nil || !updatedB.RemovedAt.Equal(qC.IntroducedAt) {
			t.Errorf("B removedAt = %v, want %v", updatedB.RemovedAt, qC.IntroducedAt)
		}

		storedA, _ := svc.GetByID(ctx, userID, hiveID, qA.ID)
		if storedA.RemovedAt == nil || !storedA.RemovedAt.Equal(newB) {
			t.Errorf("A removedAt = %v, want %v (recalculated to B's new introducedAt)", storedA.RemovedAt, newB)
		}
	})

	// Case 2: Middle queen moved before previous -> rejected
	t.Run("Case 2: Middle queen moved before previous is rejected", func(t *testing.T) {
		svc, userID, hiveID, _, qB, _ := setupChain(t)
		beforeA := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: beforeA,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid, got %v", err)
		}
	})

	// Case 3: Middle queen equal to previous -> rejected
	t.Run("Case 3: Middle queen equal to previous is rejected", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, _ := setupChain(t)

		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: qA.IntroducedAt, // exact equality with previous
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid on equal to previous, got %v", err)
		}
	})

	// Case 4: Middle queen moved after next -> rejected
	t.Run("Case 4: Middle queen moved after next is rejected", func(t *testing.T) {
		svc, userID, hiveID, _, qB, _ := setupChain(t)
		afterC := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)

		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: afterC,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid on move after next, got %v", err)
		}
	})

	// Case 5: Middle queen equal to next -> rejected
	t.Run("Case 5: Middle queen equal to next is rejected", func(t *testing.T) {
		svc, userID, hiveID, _, qB, qC := setupChain(t)

		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: qC.IntroducedAt, // exact equality with next
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid on equal to next, got %v", err)
		}
	})

	// Case 6: Oldest queen moved while still before next -> succeeds
	t.Run("Case 6: Oldest queen moved before next succeeds", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, _ := setupChain(t)
		newA := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC) // still before B (2026-01-01)

		updatedA, err := svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			Year:         2025,
			IntroducedAt: newA,
		})
		if err != nil {
			t.Fatalf("update oldest within bounds failed: %v", err)
		}
		if !updatedA.IntroducedAt.Equal(newA) {
			t.Errorf("A introducedAt = %v, want %v", updatedA.IntroducedAt, newA)
		}
		if updatedA.RemovedAt == nil || !updatedA.RemovedAt.Equal(qB.IntroducedAt) {
			t.Errorf("A removedAt = %v, want %v (must remain equal to next introducedAt)", updatedA.RemovedAt, qB.IntroducedAt)
		}
	})

	// Case 7: Oldest queen equal/after next -> rejected
	t.Run("Case 7: Oldest queen equal or after next is rejected", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, _ := setupChain(t)

		// Equal to B
		_, err := svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			Year:         2025,
			IntroducedAt: qB.IntroducedAt,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for oldest equal to next, got %v", err)
		}

		// After B
		afterB := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		_, err = svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			Year:         2025,
			IntroducedAt: afterB,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for oldest after next, got %v", err)
		}
	})

	// Case 8: Latest/current queen moved while still after previous -> succeeds
	t.Run("Case 8: Latest queen moved after previous succeeds", func(t *testing.T) {
		svc, userID, hiveID, _, qB, qC := setupChain(t)
		newC := time.Date(2027, 8, 1, 0, 0, 0, 0, time.UTC) // still after B (2026-01-01)

		updatedC, err := svc.Update(ctx, userID, "token", hiveID, qC.ID, appqueen.UpdateInput{
			Year:         2027,
			IntroducedAt: newC,
		})
		if err != nil {
			t.Fatalf("update latest queen failed: %v", err)
		}
		if !updatedC.IntroducedAt.Equal(newC) {
			t.Errorf("C introducedAt = %v, want %v", updatedC.IntroducedAt, newC)
		}
		if updatedC.RemovedAt != nil {
			t.Errorf("latest queen must remain current (removedAt == nil)")
		}

		storedB, _ := svc.GetByID(ctx, userID, hiveID, qB.ID)
		if storedB.RemovedAt == nil || !storedB.RemovedAt.Equal(newC) {
			t.Errorf("B removedAt = %v, want %v (recalculated to C's new introducedAt)", storedB.RemovedAt, newC)
		}
	})

	// Case 9: Latest/current queen equal/before previous -> rejected
	t.Run("Case 9: Latest queen equal or before previous is rejected", func(t *testing.T) {
		svc, userID, hiveID, _, qB, qC := setupChain(t)

		// Equal to B
		_, err := svc.Update(ctx, userID, "token", hiveID, qC.ID, appqueen.UpdateInput{
			Year:         2027,
			IntroducedAt: qB.IntroducedAt,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for latest equal to previous, got %v", err)
		}

		// Before B
		beforeB := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		_, err = svc.Update(ctx, userID, "token", hiveID, qC.ID, appqueen.UpdateInput{
			Year:         2027,
			IntroducedAt: beforeB,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for latest before previous, got %v", err)
		}
	})

	// Case 10: Single queen -> introducedAt may be changed freely
	t.Run("Case 10: Single queen introducedAt changed freely", func(t *testing.T) {
		svc, _, _, userID, hiveID := setupTest(t)
		initialDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		q, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{
			Year:         2026,
			IntroducedAt: initialDate,
		})
		if err != nil {
			t.Fatalf("create single queen: %v", err)
		}

		// Change to past
		pastDate := time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC)
		up1, err := svc.Update(ctx, userID, "token", hiveID, q.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: pastDate,
		})
		if err != nil {
			t.Fatalf("update single queen to past failed: %v", err)
		}
		if !up1.IntroducedAt.Equal(pastDate) || up1.RemovedAt != nil {
			t.Errorf("unexpected up1 state: %+v", up1)
		}

		// Change to future
		futureDate := time.Date(2035, 10, 1, 0, 0, 0, 0, time.UTC)
		up2, err := svc.Update(ctx, userID, "token", hiveID, q.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: futureDate,
		})
		if err != nil {
			t.Fatalf("update single queen to future failed: %v", err)
		}
		if !up2.IntroducedAt.Equal(futureDate) || up2.RemovedAt != nil {
			t.Errorf("unexpected up2 state: %+v", up2)
		}
	})

	// Case 12: Updating introducedAt never changes the queen's position/order in chain
	t.Run("Case 12: Updating introducedAt preserves relative chain order", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, qC := setupChain(t)

		// History before edit: C (2027), B (2026), A (2025)
		hBefore, _ := svc.ListHistory(ctx, userID, hiveID)
		if hBefore[0].ID != qC.ID || hBefore[1].ID != qB.ID || hBefore[2].ID != qA.ID {
			t.Fatalf("initial order unexpected: %v", hBefore)
		}

		// Valid edit within bounds
		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("valid update failed: %v", err)
		}

		// History after edit must still be C, B, A
		hAfter, _ := svc.ListHistory(ctx, userID, hiveID)
		if hAfter[0].ID != qC.ID || hAfter[1].ID != qB.ID || hAfter[2].ID != qA.ID {
			t.Errorf("order altered after valid edit: %v", hAfter)
		}

		// Attempt reordering edit (B moved after C): must be rejected, preserving order
		_, err = svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for reorder attempt, got %v", err)
		}

		hAfterRejected, _ := svc.ListHistory(ctx, userID, hiveID)
		if hAfterRejected[0].ID != qC.ID || hAfterRejected[1].ID != qB.ID || hAfterRejected[2].ID != qA.ID {
			t.Errorf("order altered after rejected reorder: %v", hAfterRejected)
		}
	})

	// Case 13: Invalid update is atomic: no queen or boundary is partially modified
	t.Run("Case 13: Invalid update is atomic with no partial boundary changes", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, qC := setupChain(t)

		origA, _ := svc.GetByID(ctx, userID, hiveID, qA.ID)
		origB, _ := svc.GetByID(ctx, userID, hiveID, qB.ID)
		origC, _ := svc.GetByID(ctx, userID, hiveID, qC.ID)

		// Invalid update: B after C
		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			Year:         2026,
			IntroducedAt: time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC),
			Notes:        "Corrupted B",
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Fatalf("expected ErrTimelineInvalid, got %v", err)
		}

		// Verify A, B, C are completely unchanged
		curA, _ := svc.GetByID(ctx, userID, hiveID, qA.ID)
		curB, _ := svc.GetByID(ctx, userID, hiveID, qB.ID)
		curC, _ := svc.GetByID(ctx, userID, hiveID, qC.ID)

		if !curA.IntroducedAt.Equal(origA.IntroducedAt) || !curA.RemovedAt.Equal(*origA.RemovedAt) {
			t.Errorf("queen A modified after rejected update: %+v vs %+v", curA, origA)
		}
		if !curB.IntroducedAt.Equal(origB.IntroducedAt) || !curB.RemovedAt.Equal(*origB.RemovedAt) || curB.Notes != origB.Notes {
			t.Errorf("queen B modified after rejected update: %+v vs %+v", curB, origB)
		}
		if !curC.IntroducedAt.Equal(origC.IntroducedAt) || curC.RemovedAt != nil {
			t.Errorf("queen C modified after rejected update: %+v vs %+v", curC, origC)
		}
	})

	// Case 14: Same introducedAt in different Hives remains valid
	t.Run("Case 14: Same introducedAt in different hives is valid", func(t *testing.T) {
		svc, queens, hives, userID, hive1 := setupTest(t)
		hive2 := uuid.New()
		hives.hives[hive2] = &domainhive.Hive{
			ID:        hive2,
			UserID:    userID,
			ApiaryID:  uuid.New(),
			Name:      "Hive 2",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}

		sharedTimestamp := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		q1, err := svc.Create(ctx, userID, "token", hive1, appqueen.CreateInput{Year: 2026, IntroducedAt: sharedTimestamp})
		if err != nil {
			t.Fatalf("create in hive1 failed: %v", err)
		}
		q2, err := svc.Create(ctx, userID, "token", hive2, appqueen.CreateInput{Year: 2026, IntroducedAt: sharedTimestamp})
		if err != nil {
			t.Fatalf("create in hive2 failed with identical timestamp: %v", err)
		}

		// Update both to another shared timestamp
		anotherShared := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		up1, err := svc.Update(ctx, userID, "token", hive1, q1.ID, appqueen.UpdateInput{Year: 2026, IntroducedAt: anotherShared})
		if err != nil {
			t.Fatalf("update hive1 failed: %v", err)
		}
		up2, err := svc.Update(ctx, userID, "token", hive2, q2.ID, appqueen.UpdateInput{Year: 2026, IntroducedAt: anotherShared})
		if err != nil {
			t.Fatalf("update hive2 failed with identical timestamp: %v", err)
		}

		if !up1.IntroducedAt.Equal(anotherShared) || !up2.IntroducedAt.Equal(anotherShared) {
			t.Errorf("timestamps not updated properly")
		}
		_ = queens
	})
}

