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

// clone returns a shallow copy of q, so callers can't mutate the repo's
// internal state through the returned pointer.
func (r *fakeQueenRepo) clone(q *domainqueen.Queen) *domainqueen.Queen {
	cloned := *q
	return &cloned
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
		// q owns no replacement_reason of its own yet: predecessorReason belongs on prev's own row.
		q.ReplacementReason = nil
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
		// q owns no replacement_reason of its own yet: predecessorReason belongs on prev's own row.
		q.ReplacementReason = nil
	}

	// Stored queen row in DB physically stores replacement_reason = nil
	stored := *q
	stored.ReplacementReason = nil
	r.queens[q.ID] = &stored
	return nil
}

func (r *fakeQueenRepo) GetCurrentByHiveID(ctx context.Context, hiveID uuid.UUID) (*domainqueen.Queen, error) {
	for _, q := range r.queens {
		if q.HiveID == hiveID && q.RemovedAt == nil {
			return r.clone(q), nil
		}
	}
	return nil, domainqueen.ErrNotFound
}

func (r *fakeQueenRepo) GetByID(ctx context.Context, hiveID, queenID uuid.UUID) (*domainqueen.Queen, error) {
	q, ok := r.queens[queenID]
	if !ok || q.HiveID != hiveID {
		return nil, domainqueen.ErrNotFound
	}
	return r.clone(q), nil
}

func (r *fakeQueenRepo) ListHistoryByHiveID(ctx context.Context, hiveID uuid.UUID) ([]*domainqueen.Queen, error) {
	list := r.getOrdered(hiveID)
	res := make([]*domainqueen.Queen, len(list))
	for i := range list {
		res[i] = r.clone(list[i])
	}
	// reverse to newest first
	for i, j := 0, len(res)-1; i < j; i, j = i+1, j-1 {
		res[i], res[j] = res[j], res[i]
	}
	return res, nil
}

func (r *fakeQueenRepo) UpdateInChain(ctx context.Context, hiveID, queenID uuid.UUID, markedAt time.Time, introducedAt time.Time, replacementReason *domainqueen.ReplacementReason, hasReplacementReason bool, notes string) (*domainqueen.Queen, error) {
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

	// Oldest queen has no incoming transition
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
	target.MarkedAt = markedAt
	target.IntroducedAt = introducedAt
	target.Notes = notes
	target.UpdatedAt = time.Now().UTC()
	// target's own replacement_reason column is untouched by this update - it
	// describes why the TARGET itself was later replaced, unrelated to hasReplacementReason.

	return r.clone(target), nil
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
	intro := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)

	q, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		MarkedAt:     intro,
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

// TestService_IntroducedAtNotInFuture verifies that Create and Update both
// reject an introducedAt that falls on a calendar day after today, while
// today itself remains valid, and that the failure surfaces as
// ErrIntroducedAtInFuture.
func TestService_IntroducedAtNotInFuture(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	tomorrow := today.AddDate(0, 0, 1)
	farFuture := today.AddDate(1, 0, 0)

	// Create: tomorrow is rejected.
	if _, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		MarkedAt:     today,
		IntroducedAt: tomorrow,
	}); !errors.Is(err, appqueen.ErrIntroducedAtInFuture) {
		t.Fatalf("Create tomorrow: got %v, want ErrIntroducedAtInFuture", err)
	}

	// Create: far future is rejected.
	if _, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		MarkedAt:     today,
		IntroducedAt: farFuture,
	}); !errors.Is(err, appqueen.ErrIntroducedAtInFuture) {
		t.Fatalf("Create far future: got %v, want ErrIntroducedAtInFuture", err)
	}

	// Create: today itself is valid.
	q, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		MarkedAt:     today,
		IntroducedAt: today,
	})
	if err != nil {
		t.Fatalf("Create today: unexpected error: %v", err)
	}

	// Update: tomorrow is rejected.
	if _, err := svc.Update(context.Background(), userID, "token", hiveID, q.ID, appqueen.UpdateInput{
		MarkedAt:     today,
		IntroducedAt: tomorrow,
	}); !errors.Is(err, appqueen.ErrIntroducedAtInFuture) {
		t.Fatalf("Update tomorrow: got %v, want ErrIntroducedAtInFuture", err)
	}

	// Update: today itself remains valid.
	if _, err := svc.Update(context.Background(), userID, "token", hiveID, q.ID, appqueen.UpdateInput{
		MarkedAt:     today,
		IntroducedAt: today,
	}); err != nil {
		t.Fatalf("Update today: unexpected error: %v", err)
	}
}

func TestService_AppendNewerQueen_ClosesPrevious(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	introA := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
	introB := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

	qA, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		MarkedAt:     introA,
		IntroducedAt: introA,
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}

	qB, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		MarkedAt:     introB,
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
	tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB})
	qC, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC})

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
	tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
	qC, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC})

	// Retroactively insert B between A and C
	qB, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB})
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
	tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)
	tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)

	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC})

	// Insert A before B
	qA, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
	if err != nil {
		t.Fatalf("insert oldest A: %v", err)
	}

	if qA.RemovedAt == nil || !qA.RemovedAt.Equal(tB) {
		t.Errorf("qA removed_at = %v, want %v", qA.RemovedAt, tB)
	}
}

func TestService_DuplicateIntroducedAt_Rejected(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	intro := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: intro, IntroducedAt: intro})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: intro, IntroducedAt: intro})
	if !errors.Is(err, domainqueen.ErrDuplicateIntroducedAt) {
		t.Errorf("got %v, want ErrDuplicateIntroducedAt", err)
	}
}

func TestService_UpdateIntroducedAt_WithinBounds(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC})

	// Move B to 2026-06-01 (strictly between 2025-01-01 and 2027-01-01)
	newTB := time.Date(2016, 6, 1, 0, 0, 0, 0, time.UTC)
	updatedB, err := svc.Update(context.Background(), userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
		MarkedAt:     newTB,
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
	tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC})

	// Try moving B before A (2024-12-01)
	invalidBefore := time.Date(2014, 12, 1, 0, 0, 0, 0, time.UTC)
	_, err := svc.Update(context.Background(), userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
		MarkedAt:     invalidBefore,
		IntroducedAt: invalidBefore,
	})
	if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
		t.Errorf("expected ErrTimelineInvalid when moving before previous, got: %v", err)
	}

	// Try moving B after C (2028-01-01)
	invalidAfter := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = svc.Update(context.Background(), userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
		MarkedAt:     invalidAfter,
		IntroducedAt: invalidAfter,
	})
	if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
		t.Errorf("expected ErrTimelineInvalid when moving after successor, got: %v", err)
	}
}

func TestService_DeleteLatest_RollbackPredecessor(t *testing.T) {
	svc, _, _, userID, hiveID := setupTest(t)
	tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB})
	qC, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC})

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
	tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

	qA, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
	qB, _ := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB})
	svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC})

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
	intro := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := svc.Create(context.Background(), userID, "token", hiveID, appqueen.CreateInput{
		MarkedAt:     intro,
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
		tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA, Notes: "Queen A"})
		if err != nil {
			t.Fatalf("create A: %v", err)
		}
		qB, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tB, IntroducedAt: tB, Notes: "Queen B"})
		if err != nil {
			t.Fatalf("create B: %v", err)
		}
		qC, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC, Notes: "Queen C"})
		if err != nil {
			t.Fatalf("create C: %v", err)
		}
		return svc, userID, hiveID, qA, qB, qC
	}

	// Case 1 & 11: Middle queen moved within bounds -> succeeds and recalculates predecessor's removed_at
	t.Run("Case 1 & 11: Middle queen within bounds updates predecessor removed_at", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, qC := setupChain(t)
		newB := time.Date(2016, 4, 1, 0, 0, 0, 0, time.UTC)

		updatedB, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:     newB,
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
		beforeA := time.Date(2014, 12, 1, 0, 0, 0, 0, time.UTC)

		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:     beforeA,
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
			MarkedAt:     qA.IntroducedAt,
			IntroducedAt: qA.IntroducedAt, // exact equality with previous
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid on equal to previous, got %v", err)
		}
	})

	// Case 4: Middle queen moved after next -> rejected
	t.Run("Case 4: Middle queen moved after next is rejected", func(t *testing.T) {
		svc, userID, hiveID, _, qB, _ := setupChain(t)
		afterC := time.Date(2017, 6, 1, 0, 0, 0, 0, time.UTC)

		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:     afterC,
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
			MarkedAt:     qC.IntroducedAt,
			IntroducedAt: qC.IntroducedAt, // exact equality with next
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid on equal to next, got %v", err)
		}
	})

	// Case 6: Oldest queen moved while still before next -> succeeds
	t.Run("Case 6: Oldest queen moved before next succeeds", func(t *testing.T) {
		svc, userID, hiveID, qA, qB, _ := setupChain(t)
		newA := time.Date(2015, 6, 1, 0, 0, 0, 0, time.UTC) // still before B (2026-01-01)

		updatedA, err := svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			MarkedAt:     newA,
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
			MarkedAt:     qB.IntroducedAt,
			IntroducedAt: qB.IntroducedAt,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for oldest equal to next, got %v", err)
		}

		// After B
		afterB := time.Date(2016, 6, 1, 0, 0, 0, 0, time.UTC)
		_, err = svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			MarkedAt:     afterB,
			IntroducedAt: afterB,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for oldest after next, got %v", err)
		}
	})

	// Case 8: Latest/current queen moved while still after previous -> succeeds
	t.Run("Case 8: Latest queen moved after previous succeeds", func(t *testing.T) {
		svc, userID, hiveID, _, qB, qC := setupChain(t)
		newC := time.Date(2017, 8, 1, 0, 0, 0, 0, time.UTC) // still after B (2026-01-01)

		updatedC, err := svc.Update(ctx, userID, "token", hiveID, qC.ID, appqueen.UpdateInput{
			MarkedAt:     newC,
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
			MarkedAt:     qB.IntroducedAt,
			IntroducedAt: qB.IntroducedAt,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for latest equal to previous, got %v", err)
		}

		// Before B
		beforeB := time.Date(2015, 6, 1, 0, 0, 0, 0, time.UTC)
		_, err = svc.Update(ctx, userID, "token", hiveID, qC.ID, appqueen.UpdateInput{
			MarkedAt:     beforeB,
			IntroducedAt: beforeB,
		})
		if !errors.Is(err, domainqueen.ErrTimelineInvalid) {
			t.Errorf("expected ErrTimelineInvalid for latest before previous, got %v", err)
		}
	})

	// Case 10: Single queen -> introducedAt may be changed freely
	t.Run("Case 10: Single queen introducedAt changed freely", func(t *testing.T) {
		svc, _, _, userID, hiveID := setupTest(t)
		initialDate := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		q, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{
			MarkedAt:     initialDate,
			IntroducedAt: initialDate,
		})
		if err != nil {
			t.Fatalf("create single queen: %v", err)
		}

		// Change to past
		pastDate := time.Date(2010, 5, 1, 0, 0, 0, 0, time.UTC)
		up1, err := svc.Update(ctx, userID, "token", hiveID, q.ID, appqueen.UpdateInput{
			MarkedAt:     pastDate,
			IntroducedAt: pastDate,
		})
		if err != nil {
			t.Fatalf("update single queen to past failed: %v", err)
		}
		if !up1.IntroducedAt.Equal(pastDate) || up1.RemovedAt != nil {
			t.Errorf("unexpected up1 state: %+v", up1)
		}

		// Change to future
		futureDate := time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)
		up2, err := svc.Update(ctx, userID, "token", hiveID, q.ID, appqueen.UpdateInput{
			MarkedAt:     futureDate,
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
			MarkedAt:     time.Date(2016, 8, 1, 0, 0, 0, 0, time.UTC),
			IntroducedAt: time.Date(2016, 8, 1, 0, 0, 0, 0, time.UTC),
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
			MarkedAt:     time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC),
			IntroducedAt: time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC),
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
			MarkedAt:     time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			IntroducedAt: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
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

		sharedTimestamp := time.Date(2016, 5, 1, 0, 0, 0, 0, time.UTC)
		q1, err := svc.Create(ctx, userID, "token", hive1, appqueen.CreateInput{MarkedAt: sharedTimestamp, IntroducedAt: sharedTimestamp})
		if err != nil {
			t.Fatalf("create in hive1 failed: %v", err)
		}
		q2, err := svc.Create(ctx, userID, "token", hive2, appqueen.CreateInput{MarkedAt: sharedTimestamp, IntroducedAt: sharedTimestamp})
		if err != nil {
			t.Fatalf("create in hive2 failed with identical timestamp: %v", err)
		}

		// Update both to another shared timestamp
		anotherShared := time.Date(2016, 7, 1, 0, 0, 0, 0, time.UTC)
		up1, err := svc.Update(ctx, userID, "token", hive1, q1.ID, appqueen.UpdateInput{MarkedAt: anotherShared, IntroducedAt: anotherShared})
		if err != nil {
			t.Fatalf("update hive1 failed: %v", err)
		}
		up2, err := svc.Update(ctx, userID, "token", hive2, q2.ID, appqueen.UpdateInput{MarkedAt: anotherShared, IntroducedAt: anotherShared})
		if err != nil {
			t.Fatalf("update hive2 failed with identical timestamp: %v", err)
		}

		if !up1.IntroducedAt.Equal(anotherShared) || !up2.IntroducedAt.Equal(anotherShared) {
			t.Errorf("timestamps not updated properly")
		}
		_ = queens
	})
}

func TestService_ReplacementReason_19RegressionCases(t *testing.T) {
	ctx := context.Background()

	reasonAging := domainqueen.ReasonAgingAndWear
	reasonLowEgg := domainqueen.ReasonLowEggLaying
	reasonSupersedure := domainqueen.ReasonNaturalSupersedure
	reasonBreedChange := domainqueen.ReasonBreedChangeOrAggressiveness

	// Case 1: Create B with reason X stores X on A, not B
	t.Run("Case 1: Create B with reason X stores X on A, not B", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		qA, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		if err != nil {
			t.Fatalf("create A: %v", err)
		}
		qB, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{
			MarkedAt:          t2,
			IntroducedAt:      t2,
			ReplacementReason: &reasonLowEgg,
		})
		if err != nil {
			t.Fatalf("create B: %v", err)
		}

		// Physical storage in DB: A stores why A was replaced by B; B stores NULL
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonLowEgg {
			t.Errorf("expected physical A.replacement_reason %v, got %v", reasonLowEgg, queens.queens[qA.ID].ReplacementReason)
		}
		if queens.queens[qB.ID].ReplacementReason != nil {
			t.Errorf("expected physical B.replacement_reason nil, got %v", queens.queens[qB.ID].ReplacementReason)
		}

		// API query semantics mirror physical storage: A's own row shows why she was
		// replaced; B's own row is nil (not replaced yet).
		curA, _ := svc.GetByID(ctx, userID, hiveID, qA.ID)
		curB, _ := svc.GetByID(ctx, userID, hiveID, qB.ID)
		if curA.ReplacementReason == nil || *curA.ReplacementReason != reasonLowEgg {
			t.Errorf("expected A own reason %v, got %v", reasonLowEgg, curA.ReplacementReason)
		}
		if curB.ReplacementReason != nil {
			t.Errorf("expected B own reason nil, got %v", *curB.ReplacementReason)
		}
	})

	// Case 2: Edit B reason X -> Y updates A to Y, not B
	t.Run("Case 2: Edit B reason X -> Y updates A to Y, not B", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonLowEgg})

		upB, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             t2,
			IntroducedAt:         t2,
			ReplacementReason:    &reasonAging,
			HasReplacementReason: true,
		})
		if err != nil {
			t.Fatalf("update B: %v", err)
		}

		// Predecessor A is updated to reasonAging
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonAging {
			t.Errorf("expected physical A.replacement_reason %v, got %v", reasonAging, queens.queens[qA.ID].ReplacementReason)
		}
		// B's physical reason remains NULL
		if queens.queens[qB.ID].ReplacementReason != nil {
			t.Errorf("expected physical B.replacement_reason nil, got %v", queens.queens[qB.ID].ReplacementReason)
		}
		// Return value reflects B's own row, untouched by this edit
		if upB.ReplacementReason != nil {
			t.Errorf("expected returned B reason nil, got %v", *upB.ReplacementReason)
		}
	})

	// Case 3: Edit B reason to explicit null clears A
	t.Run("Case 3: Edit B reason to explicit null clears A", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonLowEgg})

		upB, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             t2,
			IntroducedAt:         t2,
			ReplacementReason:    nil,
			HasReplacementReason: true,
		})
		if err != nil {
			t.Fatalf("clear B incoming reason: %v", err)
		}

		if queens.queens[qA.ID].ReplacementReason != nil {
			t.Errorf("expected physical A reason cleared to nil, got %v", *queens.queens[qA.ID].ReplacementReason)
		}
		if upB.ReplacementReason != nil {
			t.Errorf("expected returned B reason nil, got %v", *upB.ReplacementReason)
		}
	})

	// Case 4: Editing B without replacementReason does not accidentally clear A
	t.Run("Case 4: Editing B without replacementReason does not accidentally clear A", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonLowEgg})

		upB, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             t2,
			IntroducedAt:         t2,
			HasReplacementReason: false, // omitted from JSON
			Notes:                "Updated notes only",
		})
		if err != nil {
			t.Fatalf("update B notes: %v", err)
		}

		// A's reason is preserved!
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonLowEgg {
			t.Errorf("expected A reason preserved as %v, got %v", reasonLowEgg, queens.queens[qA.ID].ReplacementReason)
		}
		// B's own row is untouched either way
		if upB.ReplacementReason != nil {
			t.Errorf("expected returned B reason nil, got %v", *upB.ReplacementReason)
		}
	})

	// Case 5: Edit C reason updates B, not C
	t.Run("Case 5: Edit C reason updates B, not C", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)
		t3 := time.Date(2017, 4, 20, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonAging})
		qC, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t3, IntroducedAt: t3, ReplacementReason: &reasonLowEgg})

		upC, err := svc.Update(ctx, userID, "token", hiveID, qC.ID, appqueen.UpdateInput{
			MarkedAt:             t3,
			IntroducedAt:         t3,
			ReplacementReason:    &reasonSupersedure,
			HasReplacementReason: true,
		})
		if err != nil {
			t.Fatalf("update C reason: %v", err)
		}

		// B is updated to reasonSupersedure
		if queens.queens[qB.ID].ReplacementReason == nil || *queens.queens[qB.ID].ReplacementReason != reasonSupersedure {
			t.Errorf("expected physical B.replacement_reason %v, got %v", reasonSupersedure, queens.queens[qB.ID].ReplacementReason)
		}
		// C physical remains nil
		if queens.queens[qC.ID].ReplacementReason != nil {
			t.Errorf("expected physical C.replacement_reason nil, got %v", queens.queens[qC.ID].ReplacementReason)
		}
		// A's reason is untouched
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonAging {
			t.Errorf("expected physical A.replacement_reason %v, got %v", reasonAging, queens.queens[qA.ID].ReplacementReason)
		}
		// C's own row is untouched by this edit (it updates B, not C)
		if upC.ReplacementReason != nil {
			t.Errorf("expected returned C reason nil, got %v", *upC.ReplacementReason)
		}
	})

	// Case 6: Current C may provide replacementReason through PUT because it controls B->C
	t.Run("Case 6: Current C may provide replacementReason through PUT", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2})

		// qB is current (RemovedAt == nil). Editing with replacementReason succeeds!
		// It writes A's own row (the predecessor), not B's.
		upB, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             t2,
			IntroducedAt:         t2,
			ReplacementReason:    &reasonBreedChange,
			HasReplacementReason: true,
		})
		if err != nil {
			t.Fatalf("PUT on current queen with replacementReason must succeed, got %v", err)
		}
		if upB.ReplacementReason != nil {
			t.Errorf("expected returned B reason nil, got %v", *upB.ReplacementReason)
		}
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonBreedChange {
			t.Errorf("expected physical A.replacement_reason %v, got %v", reasonBreedChange, queens.queens[qA.ID].ReplacementReason)
		}
	})

	// Case 7: C itself always remains replacement_reason = NULL while current
	t.Run("Case 7: C itself always remains replacement_reason = NULL while current", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		_, _ = svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonLowEgg})

		_, _ = svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             t2,
			IntroducedAt:         t2,
			ReplacementReason:    &reasonSupersedure,
			HasReplacementReason: true,
		})

		if queens.queens[qB.ID].RemovedAt != nil {
			t.Errorf("expected B removed_at nil, got %v", queens.queens[qB.ID].RemovedAt)
		}
		if queens.queens[qB.ID].ReplacementReason != nil {
			t.Errorf("expected physical B replacement_reason nil, got %v", queens.queens[qB.ID].ReplacementReason)
		}
	})

	// Case 8: Oldest A cannot receive a non-null incoming replacement reason
	t.Run("Case 8: Oldest A cannot receive a non-null incoming replacement reason", func(t *testing.T) {
		svc, _, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		_, _ = svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2})

		// Editing A with non-null replacementReason must fail
		_, err := svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			MarkedAt:             t1,
			IntroducedAt:         t1,
			ReplacementReason:    &reasonAging,
			HasReplacementReason: true,
		})
		if !errors.Is(err, appqueen.ErrReplacementReasonNotAllowed) {
			t.Fatalf("expected ErrReplacementReasonNotAllowed when editing oldest queen with reason, got %v", err)
		}

		// Editing A with omitted or null reason succeeds
		_, err = svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			MarkedAt:             t1,
			IntroducedAt:         t1,
			ReplacementReason:    nil,
			HasReplacementReason: true,
		})
		if err != nil {
			t.Fatalf("editing oldest queen with null reason should succeed: %v", err)
		}
	})

	t.Run("Case 8b: edit oldest A preserves A to B replacement reason in A to B to C", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)
		t3 := time.Date(2017, 4, 20, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{
			MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonLowEgg,
		})
		qC, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t3, IntroducedAt: t3})

		updatedA, err := svc.Update(ctx, userID, "token", hiveID, qA.ID, appqueen.UpdateInput{
			MarkedAt:             t1,
			IntroducedAt:         t1,
			Notes:                "A edited",
			HasReplacementReason: false, // Flutter omits the transition field when it is unchanged.
		})
		if err != nil {
			t.Fatalf("editing A with an existing A->B reason should succeed: %v", err)
		}

		if updatedA.Notes != "A edited" {
			t.Fatalf("updated A notes = %q, want %q", updatedA.Notes, "A edited")
		}
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonLowEgg {
			t.Fatalf("A->B replacement reason was not preserved: %v", queens.queens[qA.ID].ReplacementReason)
		}
		if queens.queens[qA.ID].RemovedAt == nil || !queens.queens[qA.ID].RemovedAt.Equal(t2) {
			t.Fatalf("A removed_at = %v, want %v", queens.queens[qA.ID].RemovedAt, t2)
		}
		if queens.queens[qB.ID].RemovedAt == nil || !queens.queens[qB.ID].RemovedAt.Equal(t3) {
			t.Fatalf("B removed_at = %v, want %v", queens.queens[qB.ID].RemovedAt, t3)
		}
		if queens.queens[qC.ID].RemovedAt != nil {
			t.Fatalf("C should remain current, removed_at = %v", queens.queens[qC.ID].RemovedAt)
		}
	})

	// Case 9: Edit B introducedAt within bounds still updates A.removed_at correctly
	t.Run("Case 9: Edit B introducedAt within bounds still updates A.removed_at correctly", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		t3 := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonAging})
		_, _ = svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t3, IntroducedAt: t3})

		newBIntro := time.Date(2016, 6, 1, 0, 0, 0, 0, time.UTC)
		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             newBIntro,
			IntroducedAt:         newBIntro,
			HasReplacementReason: false,
		})
		if err != nil {
			t.Fatalf("update B date: %v", err)
		}

		if queens.queens[qA.ID].RemovedAt == nil || !queens.queens[qA.ID].RemovedAt.Equal(newBIntro) {
			t.Errorf("expected A.removed_at %v, got %v", newBIntro, queens.queens[qA.ID].RemovedAt)
		}
	})

	// Case 10: Edit B introducedAt + replacement reason atomically updates A boundary and A reason
	t.Run("Case 10: Edit B introducedAt + replacement reason atomically updates A boundary and A reason", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		t3 := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonAging})
		_, _ = svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t3, IntroducedAt: t3})

		newBIntro := time.Date(2016, 7, 1, 0, 0, 0, 0, time.UTC)
		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             newBIntro,
			IntroducedAt:         newBIntro,
			ReplacementReason:    &reasonBreedChange,
			HasReplacementReason: true,
		})
		if err != nil {
			t.Fatalf("atomic update B: %v", err)
		}

		if queens.queens[qA.ID].RemovedAt == nil || !queens.queens[qA.ID].RemovedAt.Equal(newBIntro) {
			t.Errorf("expected A.removed_at %v, got %v", newBIntro, queens.queens[qA.ID].RemovedAt)
		}
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonBreedChange {
			t.Errorf("expected A.replacement_reason %v, got %v", reasonBreedChange, queens.queens[qA.ID].ReplacementReason)
		}
	})

	// Case 11: Invalid B introducedAt leaves A reason and all chain boundaries unchanged
	t.Run("Case 11: Invalid B introducedAt leaves A reason and chain boundaries unchanged", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonAging})

		_, err := svc.Update(ctx, userID, "token", hiveID, qB.ID, appqueen.UpdateInput{
			MarkedAt:             time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
			IntroducedAt:         time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
			ReplacementReason:    &reasonLowEgg,
			HasReplacementReason: true,
		})
		if err == nil {
			t.Fatalf("expected update error")
		}

		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonAging {
			t.Errorf("A reason changed on failed update: %v", queens.queens[qA.ID].ReplacementReason)
		}
		if !queens.queens[qA.ID].RemovedAt.Equal(t2) {
			t.Errorf("A removed_at changed on failed update: %v", queens.queens[qA.ID].RemovedAt)
		}
	})

	// Case 12 & 13: DELETE C clears both B.removed_at and B.replacement_reason (B becomes current with both fields NULL)
	t.Run("Case 12 & 13: DELETE C clears both B.removed_at and B.replacement_reason", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		t3 := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonAging})
		qC, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t3, IntroducedAt: t3, ReplacementReason: &reasonLowEgg})

		if err := svc.Delete(ctx, userID, "token", hiveID, qC.ID); err != nil {
			t.Fatalf("delete C: %v", err)
		}

		// Physical row B has removed_at = NULL, replacement_reason = NULL
		if queens.queens[qB.ID].RemovedAt != nil {
			t.Errorf("expected physical B.removed_at nil, got %v", queens.queens[qB.ID].RemovedAt)
		}
		if queens.queens[qB.ID].ReplacementReason != nil {
			t.Errorf("expected physical B.replacement_reason nil, got %v", queens.queens[qB.ID].ReplacementReason)
		}
		// A's transition to B is preserved!
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonAging {
			t.Errorf("expected A.replacement_reason %v preserved, got %v", reasonAging, queens.queens[qA.ID].ReplacementReason)
		}
	})

	// Case 14: Repeated DELETE C -> DELETE B clears each removed transition's reason correctly
	t.Run("Case 14: Repeated DELETE C -> DELETE B clears each removed transition reason correctly", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		t3 := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonAging})
		qC, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t3, IntroducedAt: t3, ReplacementReason: &reasonLowEgg})

		// DELETE C: B becomes current, B's physical reason cleared, A's reason preserved
		_ = svc.Delete(ctx, userID, "token", hiveID, qC.ID)
		if queens.queens[qB.ID].ReplacementReason != nil {
			t.Errorf("expected B physical reason nil after delete C")
		}
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonAging {
			t.Errorf("expected A physical reason preserved after delete C")
		}

		// DELETE B: A becomes current, A's physical reason cleared
		_ = svc.Delete(ctx, userID, "token", hiveID, qB.ID)
		if queens.queens[qA.ID].ReplacementReason != nil {
			t.Errorf("expected A physical reason nil after delete B")
		}
		if queens.queens[qA.ID].RemovedAt != nil {
			t.Errorf("expected A removed_at nil after delete B")
		}

		// DELETE A: history empty
		_ = svc.Delete(ctx, userID, "token", hiveID, qA.ID)
		history, _ := svc.ListHistory(ctx, userID, hiveID)
		if len(history) != 0 {
			t.Errorf("expected history length 0, got %d", len(history))
		}
	})

	// Case 15: Retroactive middle insertion does not transfer an obsolete A->C reason to B->C
	t.Run("Case 15: Retroactive middle insertion does not transfer obsolete A->C reason to B->C", func(t *testing.T) {
		svc, queens, _, userID, hiveID := setupTest(t)
		tA := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		tC := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)
		tB := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tA, IntroducedAt: tA})
		qC, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: tC, IntroducedAt: tC, ReplacementReason: &reasonSupersedure})

		// Insert B between A and C with LOW_EGG_LAYING
		qB, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{
			MarkedAt:          tB,
			IntroducedAt:      tB,
			ReplacementReason: &reasonLowEgg,
		})
		if err != nil {
			t.Fatalf("insert B in middle: %v", err)
		}

		// A's physical reason updated to LOW_EGG_LAYING (why A was replaced by B)
		if queens.queens[qA.ID].ReplacementReason == nil || *queens.queens[qA.ID].ReplacementReason != reasonLowEgg {
			t.Errorf("expected A reason %v, got %v", reasonLowEgg, queens.queens[qA.ID].ReplacementReason)
		}
		// B's physical reason must NOT be copied from old A->C; it must be NULL
		if queens.queens[qB.ID].ReplacementReason != nil {
			t.Errorf("expected B physical reason nil, got %v", queens.queens[qB.ID].ReplacementReason)
		}
		// Querying C shows its own reason: nil (C is current, never replaced)
		curC, _ := svc.GetByID(ctx, userID, hiveID, qC.ID)
		if curC.ReplacementReason != nil {
			t.Errorf("expected C own reason nil, got %v", *curC.ReplacementReason)
		}
		// Querying B shows its own reason: nil (B was just inserted, not replaced yet)
		curB, _ := svc.GetByID(ctx, userID, hiveID, qB.ID)
		if curB.ReplacementReason != nil {
			t.Errorf("expected B own reason nil, got %v", *curB.ReplacementReason)
		}
	})

	// Case 16: Invalid enum remains rejected
	t.Run("Case 16: Invalid enum remains rejected", func(t *testing.T) {
		svc, _, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 4, 10, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 5, 15, 0, 0, 0, 0, time.UTC)

		_, _ = svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})

		invalidReason := domainqueen.ReplacementReason("INVALID_REASON")
		_, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{
			MarkedAt:          t2,
			IntroducedAt:      t2,
			ReplacementReason: &invalidReason,
		})
		if !errors.Is(err, appqueen.ErrReplacementReasonInvalid) {
			t.Fatalf("expected ErrReplacementReasonInvalid, got %v", err)
		}
	})

	// Case 17: (hive_id, introduced_at) and strict neighbor bounds remain unchanged
	t.Run("Case 17: duplicate introduced_at and neighbor bounds remain strictly enforced", func(t *testing.T) {
		svc, _, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		_, _ = svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})

		// Duplicate introduced_at rejected
		_, err := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		if !errors.Is(err, appqueen.ErrDuplicateIntroducedAt) {
			t.Fatalf("expected duplicate introduced_at rejected: %v", err)
		}
	})

	// Case 18: Parent Hive hard-delete cascade remains unchanged
	t.Run("Case 18: Parent Hive hard-delete cascade remains unchanged", func(t *testing.T) {
		// Schema level ON DELETE CASCADE on hive_id foreign key in migrations/000007_create_hive_queens_table.up.sql
		// and verified by PostgreSQL integration test TestHiveRepository_HardDelete_CascadesHiveQueens.
	})

	// Case 19: API GET/PUT semantics expose each queen's OWN replacement reason (mirroring
	// physical storage), and editing "the reason for this transition" via PUT always writes
	// the predecessor's row, never the target's own.
	t.Run("Case 19: API GET/PUT semantics expose each queen's own replacement reason", func(t *testing.T) {
		svc, _, _, userID, hiveID := setupTest(t)
		t1 := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		t3 := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

		qA, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t1, IntroducedAt: t1})
		qB, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t2, IntroducedAt: t2, ReplacementReason: &reasonAging})
		qC, _ := svc.Create(ctx, userID, "token", hiveID, appqueen.CreateInput{MarkedAt: t3, IntroducedAt: t3, ReplacementReason: &reasonLowEgg})
		// Physical state so far: A.own = reasonAging (why A was replaced by B),
		// B.own = reasonLowEgg (why B was replaced by C), C.own = nil (current).

		// 1. GetCurrent returns current queen C, whose own reason is always nil while current.
		current, err := svc.GetCurrent(ctx, userID, hiveID)
		if err != nil {
			t.Fatalf("get current: %v", err)
		}
		if current.ID != qC.ID || current.ReplacementReason != nil {
			t.Errorf("expected current queen C with own reason nil, got %+v", current)
		}

		// 2. ListHistory returns all queens with their own reasons.
		history, err := svc.ListHistory(ctx, userID, hiveID)
		if err != nil {
			t.Fatalf("list history: %v", err)
		}
		// newest first: C, B, A
		if len(history) != 3 {
			t.Fatalf("expected 3 queens in history, got %d", len(history))
		}
		if history[0].ReplacementReason != nil {
			t.Errorf("expected history[0] (C) own reason nil, got %v", *history[0].ReplacementReason)
		}
		if history[1].ReplacementReason == nil || *history[1].ReplacementReason != reasonLowEgg {
			t.Errorf("expected history[1] (B) own reason %v, got %v", reasonLowEgg, history[1].ReplacementReason)
		}
		if history[2].ReplacementReason == nil || *history[2].ReplacementReason != reasonAging {
			t.Errorf("expected history[2] (A) own reason %v, got %v", reasonAging, history[2].ReplacementReason)
		}

		// 3. Edit current queen C's replacementReason: this describes why B (C's predecessor)
		// was replaced, so it writes B's own row, not C's - C's own row stays nil.
		upC, err := svc.Update(ctx, userID, "token", hiveID, qC.ID, appqueen.UpdateInput{
			MarkedAt:             t3,
			IntroducedAt:         t3,
			ReplacementReason:    &reasonSupersedure,
			HasReplacementReason: true,
		})
		if err != nil {
			t.Fatalf("edit current queen C: %v", err)
		}
		if upC.ReplacementReason != nil {
			t.Errorf("expected returned C own reason nil, got %v", *upC.ReplacementReason)
		}

		// 4. GetCurrent still returns C with own reason nil; the edit is visible on B instead.
		curAfterEdit, _ := svc.GetCurrent(ctx, userID, hiveID)
		if curAfterEdit.ReplacementReason != nil {
			t.Errorf("expected GetCurrent own reason nil, got %v", *curAfterEdit.ReplacementReason)
		}
		updatedB, _ := svc.GetByID(ctx, userID, hiveID, qB.ID)
		if updatedB.ReplacementReason == nil || *updatedB.ReplacementReason != reasonSupersedure {
			t.Errorf("expected B own reason updated to %v, got %v", reasonSupersedure, updatedB.ReplacementReason)
		}
		_ = qA
	})
}
