//go:build integration

package postgres_test

// Verifies WritableIDs' and CountByApiary's real-SQL behavior: the same
// deterministic (created_at, id) ordering and per-apiary scoping proven
// against the fake repository in application/hive's unit tests, now
// against real PostgreSQL.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
)

func TestHiveRepository_WritableIDs_OldestFirstWithinApiary(t *testing.T) {
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

	ids, err := repo.WritableIDs(ctx, apiaryID, 2)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != oldest.ID || ids[1] != middle.ID {
		t.Fatalf("WritableIDs(limit=2) = %v, want [%s, %s]", ids, oldest.ID, middle.ID)
	}
}

// TestHiveRepository_WritableIDs_ScopedToApiary proves hives in a
// different apiary never affect another apiary's selection, even for the
// same user - the hierarchical rule that a hive's rank is only ever
// evaluated among its own apiary's siblings.
func TestHiveRepository_WritableIDs_ScopedToApiary(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		if err := repo.Create(ctx, hive.New(userID, apiaryB, uuid.NewString(), "")); err != nil {
			t.Fatalf("create in B %d: %v", i, err)
		}
	}

	ids, err := repo.WritableIDs(ctx, apiaryA, 5)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != inA.ID {
		t.Fatalf("WritableIDs(apiaryA) = %v, want [%s] - apiary B's hives must not leak in", ids, inA.ID)
	}

	count, err := repo.CountByApiary(ctx, apiaryA)
	if err != nil {
		t.Fatalf("CountByApiary(apiaryA): %v", err)
	}
	if count != 1 {
		t.Fatalf("CountByApiary(apiaryA) = %d, want 1", count)
	}
}

// TestHiveRepository_WritableIDs_ExcludesDeleted proves deleting a hive
// automatically promotes the next-oldest survivor in the same apiary.
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

	ids, err := repo.WritableIDs(ctx, apiaryID, 1)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != newer.ID {
		t.Fatalf("WritableIDs after deleting the oldest = %v, want [%s] (automatic promotion)", ids, newer.ID)
	}
}
