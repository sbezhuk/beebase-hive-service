package hive_test

// This file covers the dynamic Free/Pro writability model for hives:
// hierarchical parent-apiary transitivity, deterministic (created_at,
// id) selection scoped to the writable apiary, automatic promotion on
// delete, and Pro bypass. See application/hive.Service.isWritable.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/inspectionwarning"
	"github.com/sbezhuk/beebase-common/pagination"
	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

func newFreeServiceWith(repo *fakeRepo, verifier *fakeApiaryVerifier) *apphive.Service {
	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementFree}
	return apphive.NewService(repo, verifier, newFakeInspectionDeleter(), newFakeInspectionStatusProvider(inspectionwarning.DefaultThresholdDays), newFakeMediaClient(), subs)
}

// TestWritability_FiveHives_AllWritable is Case A/first half of Case
// (18): exactly 5 hives in the writable apiary all remain editable.
func TestWritability_FiveHives_AllWritable(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	apiaryID := uuid.New()
	verifier.allow("token", apiaryID)
	svc := newFreeServiceWith(repo, verifier)

	for i := 0; i < 5; i++ {
		if _, err := svc.Create(context.Background(), userID, "token", apphive.CreateInput{ApiaryID: apiaryID, Name: uuid.NewString()}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	list, _, err := svc.ListByApiary(context.Background(), userID, "token", apiaryID, pagination.Params{Page: 1, Limit: 10}, nil, nil, false)
	if err != nil {
		t.Fatalf("ListByApiary: %v", err)
	}
	for _, h := range list {
		if !h.Writable {
			t.Errorf("hive %s should be writable (only 5 hives, all within the limit)", h.ID)
		}
	}
}

// TestWritability_EightHives_FirstFiveWritable is the task's explicit
// scenario: with 8 hives in the writable apiary, only the oldest 5 (by
// created_at, id) stay writable; 6-8 become read-only, and their
// inspections/harvests can no longer be created or updated.
func TestWritability_EightHives_FirstFiveWritable(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	apiaryID := uuid.New()
	verifier.allow("token", apiaryID)

	hives := make([]*hive.Hive, 8)
	for i := 0; i < 8; i++ {
		h := hive.New(userID, apiaryID, uuid.NewString(), "")
		h.CreatedAt = h.CreatedAt.Add(time.Duration(i) * time.Minute)
		mustCreate(t, repo, h)
		hives[i] = h
	}

	svc := newFreeServiceWith(repo, verifier)

	for i, h := range hives {
		got, err := svc.Get(context.Background(), userID, "token", h.ID)
		if err != nil {
			t.Fatalf("Get hive %d: %v", i, err)
		}
		want := i < 5
		if got.Writable != want {
			t.Errorf("hive %d writable = %v, want %v", i, got.Writable, want)
		}
	}
}

// TestWritability_DeleteWritableHive_PromotesNextAutomatically is the
// task's explicit promotion scenario: deleting one of the first 5 hives
// must automatically make hive 6 writable, with no activation step.
func TestWritability_DeleteWritableHive_PromotesNextAutomatically(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	apiaryID := uuid.New()
	verifier.allow("token", apiaryID)

	hives := make([]*hive.Hive, 8)
	for i := 0; i < 8; i++ {
		h := hive.New(userID, apiaryID, uuid.NewString(), "")
		h.CreatedAt = h.CreatedAt.Add(time.Duration(i) * time.Minute)
		mustCreate(t, repo, h)
		hives[i] = h
	}

	svc := newFreeServiceWith(repo, verifier)
	hive6 := hives[5]

	got, err := svc.Get(context.Background(), userID, "token", hive6.ID)
	if err != nil {
		t.Fatalf("Get hive6: %v", err)
	}
	if got.Writable {
		t.Fatal("hive6 should start read-only")
	}

	// Delete hive 2 (index 1), one of the originally-writable five.
	if err := svc.Delete(context.Background(), userID, "token", hives[1].ID); err != nil {
		t.Fatalf("Delete hive2: %v", err)
	}

	got, err = svc.Get(context.Background(), userID, "token", hive6.ID)
	if err != nil {
		t.Fatalf("Get hive6 after delete: %v", err)
	}
	if !got.Writable {
		t.Fatal("hive6 should automatically become writable once a writable sibling is deleted")
	}

	// And its inspections/harvests can now be created - proven at this
	// package's level via isWritable; the actual inspection/harvest
	// creation check lives in their own services and is covered there.
	if _, err := svc.Update(context.Background(), userID, "token", hive6.ID, apphive.UpdateInput{Name: "now editable"}); err != nil {
		t.Fatalf("Update hive6 after promotion: %v", err)
	}
}

// TestWritability_HiveUnderReadOnlyApiary_AlwaysReadOnly is Case (6):
// parent-apiary locking is transitive - a hive under a read-only apiary
// stays read-only even if the account's total hive count would otherwise
// leave room.
func TestWritability_HiveUnderReadOnlyApiary_AlwaysReadOnly(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	writableApiary := uuid.New()
	lockedApiary := uuid.New()
	verifier.allow("token", writableApiary)
	verifier.allow("token", lockedApiary)

	// 3 hives in the writable apiary, well under the limit.
	for i := 0; i < 3; i++ {
		mustCreate(t, repo, hive.New(userID, writableApiary, uuid.NewString(), ""))
	}
	lockedHive := hive.New(userID, lockedApiary, "locked hive", "")
	mustCreate(t, repo, lockedHive)
	verifier.lock(lockedApiary)

	svc := newFreeServiceWith(repo, verifier)

	got, err := svc.Get(context.Background(), userID, "token", lockedHive.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Writable {
		t.Fatal("a hive under a read-only apiary must stay read-only regardless of total hive count")
	}

	_, err = svc.Update(context.Background(), userID, "token", lockedHive.ID, apphive.UpdateInput{Name: "hijack"})
	if !errors.Is(err, apphive.ErrParentReadOnly) {
		t.Fatalf("Update under a read-only apiary: got %v, want ErrParentReadOnly", err)
	}
}

// TestWritability_HiveItselfLockedUnderWritableApiary distinguishes
// ErrReadOnly (the hive itself is outside the limit) from
// ErrParentReadOnly (the apiary is) - both are needed so a client can
// tell them apart without parsing the message.
func TestWritability_HiveItselfLockedUnderWritableApiary(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	apiaryID := uuid.New()
	verifier.allow("token", apiaryID)

	for i := 0; i < 6; i++ {
		h := hive.New(userID, apiaryID, uuid.NewString(), "")
		h.CreatedAt = h.CreatedAt.Add(time.Duration(i) * time.Minute)
		mustCreate(t, repo, h)
		if i == 5 {
			svc := newFreeServiceWith(repo, verifier)
			_, err := svc.Update(context.Background(), userID, "token", h.ID, apphive.UpdateInput{Name: "hijack"})
			if !errors.Is(err, apphive.ErrReadOnly) {
				t.Fatalf("Update on the 6th hive: got %v, want ErrReadOnly", err)
			}
		}
	}
}

// TestWritability_ProBypassesEverything proves Pro ignores both the
// hive-count ranking and parent-apiary lock state entirely.
func TestWritability_ProBypassesEverything(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	lockedApiary := uuid.New()
	verifier.allow("token", lockedApiary)
	h := hive.New(userID, lockedApiary, "H", "")
	mustCreate(t, repo, h)
	verifier.lock(lockedApiary)

	svc := newService(repo, verifier) // Pro by default

	got, err := svc.Get(context.Background(), userID, "token", h.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Writable {
		t.Fatal("under Pro, a hive under a locked apiary should still be writable")
	}
	if _, err := svc.Update(context.Background(), userID, "token", h.ID, apphive.UpdateInput{Name: "fine"}); err != nil {
		t.Fatalf("Update under Pro: %v", err)
	}
}

// TestWritability_ReupgradeImmediatelyUnlocks mirrors apiary-service's
// equivalent: re-upgrading to Pro makes a previously read-only hive
// writable on the very next request, no migration needed.
func TestWritability_ReupgradeImmediatelyUnlocks(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	apiaryID := uuid.New()
	verifier.allow("token", apiaryID)

	hives := make([]*hive.Hive, 6)
	for i := 0; i < 6; i++ {
		h := hive.New(userID, apiaryID, uuid.NewString(), "")
		h.CreatedAt = h.CreatedAt.Add(time.Duration(i) * time.Minute)
		mustCreate(t, repo, h)
		hives[i] = h
	}
	hive6 := hives[5]

	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementFree}
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), newFakeInspectionStatusProvider(inspectionwarning.DefaultThresholdDays), newFakeMediaClient(), subs)

	got, err := svc.Get(context.Background(), userID, "token", hive6.ID)
	if err != nil {
		t.Fatalf("Get while Free: %v", err)
	}
	if got.Writable {
		t.Fatal("hive6 should start read-only under Free")
	}

	subs.entitlement = apphive.EntitlementPro

	got, err = svc.Get(context.Background(), userID, "token", hive6.ID)
	if err != nil {
		t.Fatalf("Get after re-upgrade: %v", err)
	}
	if !got.Writable {
		t.Fatal("hive6 should be immediately writable after re-upgrading to Pro")
	}
}

// TestList_WritableFlag_IndependentOfSearch proves writability is derived
// from the caller's complete live hives, never from the current search
// result - searching for a normally-locked hive must not make it
// writable just because the writable ones are filtered out of view.
func TestList_WritableFlag_IndependentOfSearch(t *testing.T) {
	repo := newFakeRepo()
	verifier := newFakeApiaryVerifier()
	userID := uuid.New()
	apiaryID := uuid.New()
	verifier.allow("token", apiaryID)

	for i := 0; i < 8; i++ {
		h := hive.New(userID, apiaryID, "Hive "+uuid.NewString(), "")
		h.CreatedAt = h.CreatedAt.Add(time.Duration(i) * time.Minute)
		if i == 7 {
			h.Name = "Hive Eight Special"
		}
		mustCreate(t, repo, h)
	}

	svc := newFreeServiceWith(repo, verifier)

	search := "Special"
	list, total, err := svc.ListByApiary(context.Background(), userID, "token", apiaryID, pagination.Params{Page: 1, Limit: 10}, &search, nil, false)
	if err != nil {
		t.Fatalf("ListByApiary with search: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("search result = %d items, want exactly 1", len(list))
	}
	if list[0].Writable {
		t.Fatal("the 8th (read-only) hive must not become writable just because it's the only search result")
	}
}
