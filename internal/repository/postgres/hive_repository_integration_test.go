//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
)

func TestHiveRepository_CreateAndGet(t *testing.T) {
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

	h := hive.New(userID, apiaryID, "Hive 1", "North corner", "strong colony")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, userID, h.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ApiaryID != apiaryID {
		t.Errorf("ApiaryID = %s, want %s", got.ApiaryID, apiaryID)
	}
	if got.Name != h.Name || got.Location != h.Location || got.Notes != h.Notes {
		t.Errorf("GetByID = %+v, want fields matching %+v", got, h)
	}
}

func TestHiveRepository_GetByID_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)

	_, err = repo.GetByID(ctx, uuid.New(), uuid.New())
	if !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("GetByID for unknown hive: got %v, want ErrNotFound", err)
	}
}

// TestHiveRepository_GetByID_WrongOwner_NotFound is the real-database
// version of this module's central security guarantee: a hive that
// exists, but belongs to someone else, must be indistinguishable from one
// that doesn't exist at all.
func TestHiveRepository_GetByID_WrongOwner_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	owner := uuid.New()
	other := uuid.New()

	h := hive.New(owner, uuid.New(), "Owner's hive", "", "")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.GetByID(ctx, other, h.ID)
	if !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("GetByID by non-owner: got %v, want ErrNotFound", err)
	}
}

func TestHiveRepository_ListByUser_OnlyOwnHives(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userA := uuid.New()
	userB := uuid.New()

	for _, name := range []string{"A1", "A2"} {
		if err := repo.Create(ctx, hive.New(userA, uuid.New(), name, "", "")); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := repo.Create(ctx, hive.New(userB, uuid.New(), "B1", "", "")); err != nil {
		t.Fatalf("create B1: %v", err)
	}

	list, err := repo.ListByUser(ctx, userA)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByUser returned %d hives, want 2", len(list))
	}
	for _, h := range list {
		if h.UserID != userA {
			t.Errorf("ListByUser leaked hive %s owned by %s", h.ID, h.UserID)
		}
	}
}

func TestHiveRepository_Update(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()

	h := hive.New(userID, uuid.New(), "Old name", "Old location", "Old notes")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	h.Name = "New name"
	h.Location = "New location"
	h.Notes = "New notes"
	if err := repo.Update(ctx, h); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(ctx, userID, h.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "New name" || got.Location != "New location" || got.Notes != "New notes" {
		t.Errorf("GetByID after update = %+v, want updated fields", got)
	}
}

func TestHiveRepository_Update_WrongOwner_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	owner := uuid.New()
	other := uuid.New()

	h := hive.New(owner, uuid.New(), "Owner's hive", "", "")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	hijack := *h
	hijack.UserID = other
	hijack.Name = "Hijacked"
	if err := repo.Update(ctx, &hijack); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Update with mismatched owner: got %v, want ErrNotFound", err)
	}

	got, err := repo.GetByID(ctx, owner, h.ID)
	if err != nil {
		t.Fatalf("GetByID after failed hijack: %v", err)
	}
	if got.Name != "Owner's hive" {
		t.Errorf("Name = %q after failed hijack, want unchanged", got.Name)
	}
}

func TestHiveRepository_Delete_SoftDelete(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()

	h := hive.New(userID, uuid.New(), "Gone soon", "", "")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, userID, h.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.GetByID(ctx, userID, h.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("GetByID after delete: got %v, want ErrNotFound", err)
	}

	// The row itself must still exist (soft delete), just filtered out by
	// deleted_at IS NULL.
	var deletedAt *string
	err = tx.QueryRow(ctx, "SELECT deleted_at::text FROM hives WHERE id = $1", h.ID).Scan(&deletedAt)
	if err != nil {
		t.Fatalf("query raw row: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at is NULL after Delete; expected it to be set (soft delete)")
	}
}

func TestHiveRepository_Delete_WrongOwner_NotFoundAndNotDeleted(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	owner := uuid.New()
	other := uuid.New()

	h := hive.New(owner, uuid.New(), "Owner's hive", "", "")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, other, h.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Delete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := repo.GetByID(ctx, owner, h.ID); err != nil {
		t.Fatalf("owner's hive should survive a failed delete attempt: %v", err)
	}
}
