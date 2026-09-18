package queen_test

// This file covers dynamic Free/Pro writability enforcement for queen
// mutations: Create, Update and Delete all delegate to requireWritable,
// which mirrors the hive-service entitlement model.
//
// Rules under test:
//   - Pro: all mutations always succeed (no count or apiary gate).
//   - Free within limit: hive ranks among oldest FreeMaxHives account-wide
//     and its parent apiary is writable → mutations succeed.
//   - Free outside hive allowance: hive is beyond rank FreeMaxHives
//     → ErrReadOnly (maps to 403 resource_pro_locked).
//   - Free outside apiary allowance: parent apiary is read-only
//     → ErrParentReadOnly (maps to 403 parent_resource_pro_locked).
//   - Dynamic downgrade: subscription switches to Free mid-session
//     → next mutation on an outside-limit hive is immediately blocked.
//   - Dynamic upgrade: subscription switches back to Pro mid-session
//     → next mutation is immediately allowed again.
//   - GET endpoints (GetCurrent, GetByID, ListHistory) are NEVER blocked
//     by writability checks, regardless of entitlement.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	appqueen "github.com/sbezhuk/beebase-hive-service/internal/application/queen"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// newHive constructs a minimal hive for testing.
func newHive(userID, apiaryID uuid.UUID) *domainhive.Hive {
	return &domainhive.Hive{
		ID:        uuid.New(),
		UserID:    userID,
		ApiaryID:  apiaryID,
		Name:      "hive-" + uuid.NewString(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

// buildFreeService creates a queen service configured for Free entitlement
// with explicit writable hive IDs and apiary writability.
func buildFreeService(
	h *domainhive.Hive,
	writableHiveIDs []uuid.UUID,
	apiaryWritable bool,
	queensRepo *fakeQueenRepo,
) *appqueen.Service {
	hiveRepo := newFakeHiveRepo()
	hiveRepo.hives[h.ID] = h
	hiveRepo.writableIDs = writableHiveIDs
	return appqueen.NewService(
		queensRepo,
		hiveRepo,
		&fakeApiaryVerifier{writable: apiaryWritable},
		&fakeSubscriptionClient{entitlement: apphive.EntitlementFree},
	)
}

func buildProService(h *domainhive.Hive, queensRepo *fakeQueenRepo) *appqueen.Service {
	hiveRepo := newFakeHiveRepo()
	hiveRepo.hives[h.ID] = h
	return appqueen.NewService(
		queensRepo,
		hiveRepo,
		&fakeApiaryVerifier{writable: true},
		&fakeSubscriptionClient{entitlement: apphive.EntitlementPro},
	)
}

func mustIntro(y, m, d int) appqueen.CreateInput {
	date := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return appqueen.CreateInput{MarkedAt: date, IntroducedAt: date}
}

// ─── Pro bypass ─────────────────────────────────────────────────────────────

// TestQueen_Writability_ProBypassesHiveLimitAndApiaryLock verifies that
// Pro callers can mutate queens regardless of apiary lock state or hive
// rank — mirrors TestWritability_ProBypassesEverything in hive package.
func TestQueen_Writability_ProBypassesHiveLimitAndApiaryLock(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	// Apiary is read-only, hive NOT in writableIDs — Pro must bypass both.
	hiveRepo := newFakeHiveRepo()
	hiveRepo.hives[h.ID] = h
	hiveRepo.writableIDs = []uuid.UUID{}
	svc := appqueen.NewService(
		queensRepo, hiveRepo,
		&fakeApiaryVerifier{writable: false},
		&fakeSubscriptionClient{entitlement: apphive.EntitlementPro},
	)

	if _, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1)); err != nil {
		t.Fatalf("Pro Create: unexpected error: %v", err)
	}
}

// ─── Free — within limit ─────────────────────────────────────────────────────

// TestQueen_Writability_Free_WithinLimit_CreateSucceeds verifies that a
// Free user whose hive is within FreeMaxHives and whose apiary is writable
// can successfully introduce a queen.
func TestQueen_Writability_Free_WithinLimit_CreateSucceeds(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()
	svc := buildFreeService(h, []uuid.UUID{h.ID}, true, queensRepo)

	if _, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1)); err != nil {
		t.Fatalf("Free within limit Create: unexpected error: %v", err)
	}
}

// ─── Free — hive outside limit (ErrReadOnly) ────────────────────────────────

// TestQueen_Writability_Free_HiveOutsideLimit_CreateBlocked verifies that
// a Free user whose hive is not among the oldest FreeMaxHives cannot
// introduce a queen (apiary itself is writable).
func TestQueen_Writability_Free_HiveOutsideLimit_CreateBlocked(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()
	// writableIDs is empty → h is beyond the 5-hive limit.
	svc := buildFreeService(h, []uuid.UUID{}, true, queensRepo)

	_, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1))
	if !errors.Is(err, appqueen.ErrReadOnly) {
		t.Fatalf("Create on hive beyond limit: got %v, want ErrReadOnly", err)
	}
}

// TestQueen_Writability_Free_HiveOutsideLimit_UpdateBlocked checks that
// Update is also blocked when the hive is beyond the Free limit.
func TestQueen_Writability_Free_HiveOutsideLimit_UpdateBlocked(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	// Create via Pro.
	proSvc := buildProService(h, queensRepo)
	q, err := proSvc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1))
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}

	// Switch to Free with hive outside limit.
	freeSvc := buildFreeService(h, []uuid.UUID{}, true, queensRepo)

	_, err = freeSvc.Update(context.Background(), userID, "token", h.ID, q.ID, appqueen.UpdateInput{
		MarkedAt:     time.Date(2015, 2, 1, 0, 0, 0, 0, time.UTC),
		IntroducedAt: time.Date(2015, 2, 1, 0, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, appqueen.ErrReadOnly) {
		t.Fatalf("Update on hive beyond limit: got %v, want ErrReadOnly", err)
	}
}

// TestQueen_Writability_Free_HiveOutsideLimit_DeleteBlocked checks that
// Delete is also blocked when the hive is beyond the Free limit.
func TestQueen_Writability_Free_HiveOutsideLimit_DeleteBlocked(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	proSvc := buildProService(h, queensRepo)
	q, err := proSvc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1))
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}

	freeSvc := buildFreeService(h, []uuid.UUID{}, true, queensRepo)

	if err := freeSvc.Delete(context.Background(), userID, "token", h.ID, q.ID); !errors.Is(err, appqueen.ErrReadOnly) {
		t.Fatalf("Delete on hive beyond limit: got %v, want ErrReadOnly", err)
	}
}

// ─── Free — parent apiary locked (ErrParentReadOnly) ────────────────────────

// TestQueen_Writability_Free_ApiaryLocked_CreateBlocked verifies that a
// Free user whose parent apiary is read-only cannot introduce a queen,
// even when the hive itself ranks within the limit.
func TestQueen_Writability_Free_ApiaryLocked_CreateBlocked(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()
	// Hive IS within writable IDs but apiary is locked.
	svc := buildFreeService(h, []uuid.UUID{h.ID}, false, queensRepo)

	_, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1))
	if !errors.Is(err, appqueen.ErrParentReadOnly) {
		t.Fatalf("Create under locked apiary: got %v, want ErrParentReadOnly", err)
	}
}

// TestQueen_Writability_Free_ApiaryLocked_UpdateBlocked verifies Update is
// blocked when the parent apiary is read-only.
func TestQueen_Writability_Free_ApiaryLocked_UpdateBlocked(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	proSvc := buildProService(h, queensRepo)
	q, err := proSvc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1))
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}

	// Switch to Free with locked apiary.
	freeSvc := buildFreeService(h, []uuid.UUID{h.ID}, false, queensRepo)

	_, err = freeSvc.Update(context.Background(), userID, "token", h.ID, q.ID, appqueen.UpdateInput{
		MarkedAt:     time.Date(2015, 2, 1, 0, 0, 0, 0, time.UTC),
		IntroducedAt: time.Date(2015, 2, 1, 0, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, appqueen.ErrParentReadOnly) {
		t.Fatalf("Update under locked apiary: got %v, want ErrParentReadOnly", err)
	}
}

// TestQueen_Writability_Free_ApiaryLocked_DeleteBlocked verifies Delete is
// blocked when the parent apiary is read-only.
func TestQueen_Writability_Free_ApiaryLocked_DeleteBlocked(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	proSvc := buildProService(h, queensRepo)
	q, err := proSvc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1))
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}

	freeSvc := buildFreeService(h, []uuid.UUID{h.ID}, false, queensRepo)

	if err := freeSvc.Delete(context.Background(), userID, "token", h.ID, q.ID); !errors.Is(err, appqueen.ErrParentReadOnly) {
		t.Fatalf("Delete under locked apiary: got %v, want ErrParentReadOnly", err)
	}
}

// ─── Dynamic downgrade ──────────────────────────────────────────────────────

// TestQueen_Writability_DynamicDowngrade_ImmediatelyBlocks verifies that
// switching from Pro to Free immediately blocks mutations on a hive that
// falls outside the Free limit — no restart or migration needed.
func TestQueen_Writability_DynamicDowngrade_ImmediatelyBlocks(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementPro}
	hiveRepo := newFakeHiveRepo()
	hiveRepo.hives[h.ID] = h
	hiveRepo.writableIDs = []uuid.UUID{} // h will be outside Free limit
	svc := appqueen.NewService(queensRepo, hiveRepo, &fakeApiaryVerifier{writable: true}, subs)

	// Under Pro: Create succeeds.
	if _, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1)); err != nil {
		t.Fatalf("Pro Create: %v", err)
	}

	// Simulate subscription expiry — same service instance.
	subs.entitlement = apphive.EntitlementFree

	_, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2026, 1, 1))
	if !errors.Is(err, appqueen.ErrReadOnly) {
		t.Fatalf("after downgrade Create: got %v, want ErrReadOnly", err)
	}
}

// ─── Dynamic upgrade ────────────────────────────────────────────────────────

// TestQueen_Writability_DynamicUpgrade_ImmediatelyUnlocks verifies that
// switching from Free to Pro immediately allows mutations on a previously
// blocked hive — no restart or migration needed.
func TestQueen_Writability_DynamicUpgrade_ImmediatelyUnlocks(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementFree}
	hiveRepo := newFakeHiveRepo()
	hiveRepo.hives[h.ID] = h
	hiveRepo.writableIDs = []uuid.UUID{} // hive is outside Free limit
	svc := appqueen.NewService(queensRepo, hiveRepo, &fakeApiaryVerifier{writable: true}, subs)

	// Under Free: blocked.
	if _, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1)); !errors.Is(err, appqueen.ErrReadOnly) {
		t.Fatalf("Free Create before upgrade: got %v, want ErrReadOnly", err)
	}

	// Upgrade to Pro — same service instance.
	subs.entitlement = apphive.EntitlementPro

	if _, err := svc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1)); err != nil {
		t.Fatalf("after upgrade Create: unexpected error: %v", err)
	}
}

// ─── GET endpoints never blocked ────────────────────────────────────────────

// TestQueen_Writability_GetEndpointsNeverBlocked verifies that read
// operations are always accessible regardless of Free/Pro status, even when
// both the parent apiary and the hive itself are read-only — matching the
// spec: "read-only Hives must still expose all queen GET endpoints".
func TestQueen_Writability_GetEndpointsNeverBlocked(t *testing.T) {
	userID := uuid.New()
	h := newHive(userID, uuid.New())
	queensRepo := newFakeQueenRepo()

	// Create a queen via Pro first.
	proSvc := buildProService(h, queensRepo)
	q, err := proSvc.Create(context.Background(), userID, "token", h.ID, mustIntro(2025, 1, 1))
	if err != nil {
		t.Fatalf("Pro Create: %v", err)
	}

	// Worst-case Free: apiary locked AND hive outside limit.
	freeSvc := buildFreeService(h, []uuid.UUID{}, false, queensRepo)

	if _, err := freeSvc.GetCurrent(context.Background(), userID, h.ID); err != nil {
		t.Errorf("GetCurrent must not be blocked by writability: %v", err)
	}
	if _, err := freeSvc.GetByID(context.Background(), userID, h.ID, q.ID); err != nil {
		t.Errorf("GetByID must not be blocked by writability: %v", err)
	}
	if _, err := freeSvc.ListHistory(context.Background(), userID, h.ID); err != nil {
		t.Errorf("ListHistory must not be blocked by writability: %v", err)
	}
}
