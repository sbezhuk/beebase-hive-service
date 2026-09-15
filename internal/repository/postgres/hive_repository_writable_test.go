//go:build integration

package postgres_test

// Verifies WritableIDs' real-SQL behavior: the same deterministic
// (created_at, id) ordering, evaluated account-wide across all of a
// user's apiaries (FreeMaxHives is a per-user quota, not per-apiary),
// proven against the fake repository in application/hive's unit tests,
// now against real PostgreSQL.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
)

func TestHiveRepository_WritableIDs_OldestFirstAccountWide(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()
	apiaryID := uuid.New()

	oldest := hive.New(userID, apiaryID, "Oldest", "")
	if err := repo.Create(ctx, oldest); err != nil {
		t.Fatalf("create oldest: %v", err)
	}
	middle := hive.New(userID, apiaryID, "Middle", "")
	middle.CreatedAt = oldest.CreatedAt.Add(time.Hour)
	if err := repo.Create(ctx, middle); err != nil {
		t.Fatalf("create middle: %v", err)
	}
	newest := hive.New(userID, apiaryID, "Newest", "")
	newest.CreatedAt = oldest.CreatedAt.Add(2 * time.Hour)
	if err := repo.Create(ctx, newest); err != nil {
		t.Fatalf("create newest: %v", err)
	}

	ids, err := repo.WritableIDs(ctx, userID, 2)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != oldest.ID || ids[1] != middle.ID {
		t.Fatalf("WritableIDs(limit=2) = %v, want [%s, %s]", ids, oldest.ID, middle.ID)
	}
}

// TestHiveRepository_WritableIDs_SpansMultipleApiaries proves the
// selection pool is the user's complete set of hives across every apiary
// they own, not scoped to any single one - FreeMaxHives is an
// account-wide quota. Whether a given hive is actually writable also
// depends on its parent apiary being writable (a separate check against
// apiary-service, not exercised by this repository-level test), but the
// ranking itself must never stop at an apiary boundary.
func TestHiveRepository_WritableIDs_SpansMultipleApiaries(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()
	apiaryA := uuid.New()
	apiaryB := uuid.New()

	inA := hive.New(userID, apiaryA, "In A", "")
	if err := repo.Create(ctx, inA); err != nil {
		t.Fatalf("create in A: %v", err)
	}
	inB := make([]*hive.Hive, 3)
	for i := range inB {
		h := hive.New(userID, apiaryB, uuid.NewString(), "")
		h.CreatedAt = inA.CreatedAt.Add(time.Duration(i+1) * time.Hour)
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create in B %d: %v", i, err)
		}
		inB[i] = h
	}

	// All 4 hives (1 in A, 3 in B) must appear, oldest first, regardless
	// of apiary - a limit of 5 returns every hive the user owns.
	ids, err := repo.WritableIDs(ctx, userID, 5)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	want := []uuid.UUID{inA.ID, inB[0].ID, inB[1].ID, inB[2].ID}
	if len(ids) != len(want) {
		t.Fatalf("WritableIDs = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("WritableIDs[%d] = %s, want %s (account-wide oldest-first order)", i, ids[i], want[i])
		}
	}

	count, err := repo.CountByUser(ctx, userID)
	if err != nil {
		t.Fatalf("CountByUser: %v", err)
	}
	if count != 4 {
		t.Fatalf("CountByUser = %d, want 4 (across both apiaries)", count)
	}
}

// TestHiveRepository_WritableIDs_ExcludesDeleted proves deleting a hive
// automatically promotes the next-oldest survivor account-wide.
func TestHiveRepository_WritableIDs_ExcludesDeleted(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()
	apiaryID := uuid.New()

	oldest := hive.New(userID, apiaryID, "Oldest", "")
	if err := repo.Create(ctx, oldest); err != nil {
		t.Fatalf("create oldest: %v", err)
	}
	newer := hive.New(userID, apiaryID, "Newer", "")
	newer.CreatedAt = oldest.CreatedAt.Add(time.Hour)
	if err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}

	if err := repo.HardDelete(ctx, userID, oldest.ID); err != nil {
		t.Fatalf("HardDelete oldest: %v", err)
	}

	ids, err := repo.WritableIDs(ctx, userID, 1)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != newer.ID {
		t.Fatalf("WritableIDs after deleting the oldest = %v, want [%s] (automatic promotion)", ids, newer.ID)
	}
}
