//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/pagination"
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

	h := hive.New(userID, apiaryID, "Hive 1", "strong colony")
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
	if got.Name != h.Name || got.Notes != h.Notes {
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

	h := hive.New(owner, uuid.New(), "Owner's hive", "")
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
		if err := repo.Create(ctx, hive.New(userA, uuid.New(), name, "")); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := repo.Create(ctx, hive.New(userB, uuid.New(), "B1", "")); err != nil {
		t.Fatalf("create B1: %v", err)
	}

	list, total, err := repo.ListByUser(ctx, userA, pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if total != 2 {
		t.Fatalf("ListByUser total = %d, want 2", total)
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

func TestHiveRepository_ListByUser_Pagination(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()

	const count = 5
	for i := 0; i < count; i++ {
		if err := repo.Create(ctx, hive.New(userID, uuid.New(), "H", "")); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	// First page.
	first, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 1: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(first) != 2 {
		t.Fatalf("page 1 returned %d hives, want 2", len(first))
	}

	// Middle page.
	middle, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 2, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 2: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(middle) != 2 {
		t.Fatalf("page 2 returned %d hives, want 2", len(middle))
	}

	// Last (partial) page.
	last, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 3, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 3: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(last) != 1 {
		t.Fatalf("page 3 returned %d hives, want 1", len(last))
	}

	// Page beyond available data.
	beyond, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 10, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 10: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(beyond) != 0 {
		t.Fatalf("page beyond available data returned %d hives, want 0", len(beyond))
	}

	// Pages must not overlap and together must cover every row exactly once.
	seen := map[uuid.UUID]bool{}
	for _, h := range append(append(first, middle...), last...) {
		if seen[h.ID] {
			t.Errorf("hive %s appeared on more than one page", h.ID)
		}
		seen[h.ID] = true
	}
	if len(seen) != count {
		t.Errorf("pages together covered %d hives, want %d", len(seen), count)
	}
}

func TestHiveRepository_ListByUser_Empty(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)

	list, total, err := repo.ListByUser(ctx, uuid.New(), pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	if len(list) != 0 {
		t.Fatalf("ListByUser = %v, want empty", list)
	}
}

// TestHiveRepository_ListByUser_StableOrdering guards against equal
// created_at timestamps reshuffling rows between pages: the id tiebreaker
// must make ordering deterministic even when many hives share a timestamp
// (a real possibility, since created_at defaults from the same batch
// insert).
func TestHiveRepository_ListByUser_StableOrdering(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()

	now := time.Now().UTC()
	ids := make([]uuid.UUID, 4)
	for i := range ids {
		h := hive.New(userID, uuid.New(), "H", "")
		h.CreatedAt = now
		h.UpdatedAt = now
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids[i] = h.ID
	}

	firstRun, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 4})
	if err != nil {
		t.Fatalf("ListByUser run 1: %v", err)
	}
	secondRun, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 4})
	if err != nil {
		t.Fatalf("ListByUser run 2: %v", err)
	}

	if len(firstRun) != len(secondRun) {
		t.Fatalf("run lengths differ: %d vs %d", len(firstRun), len(secondRun))
	}
	for i := range firstRun {
		if firstRun[i].ID != secondRun[i].ID {
			t.Fatalf("ordering unstable at index %d: %s vs %s", i, firstRun[i].ID, secondRun[i].ID)
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

	h := hive.New(userID, uuid.New(), "Old name", "Old notes")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	h.Name = "New name"
	h.Notes = "New notes"
	if err := repo.Update(ctx, h); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(ctx, userID, h.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "New name" || got.Notes != "New notes" {
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

	h := hive.New(owner, uuid.New(), "Owner's hive", "")
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

func TestHiveRepository_HardDelete_Success(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()

	h := hive.New(userID, uuid.New(), "Gone soon", "")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.HardDelete(ctx, userID, h.ID); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}

	if _, err := repo.GetByID(ctx, userID, h.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("GetByID after HardDelete: got %v, want ErrNotFound", err)
	}

	// The row itself must be fully gone, not just deleted_at-marked.
	var n int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM hives WHERE id = $1", h.ID).Scan(&n); err != nil {
		t.Fatalf("raw count: %v", err)
	}
	if n != 0 {
		t.Errorf("hive still present after HardDelete; want fully removed")
	}
}

func TestHiveRepository_HardDelete_WrongOwner_NotFoundAndNotDeleted(t *testing.T) {
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

	h := hive.New(owner, uuid.New(), "Owner's hive", "")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.HardDelete(ctx, other, h.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("HardDelete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := repo.GetByID(ctx, owner, h.ID); err != nil {
		t.Fatalf("owner's hive should survive a failed delete attempt: %v", err)
	}
}

func TestHiveRepository_ListByApiary_IncludesAlreadySoftDeleted(t *testing.T) {
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
	otherApiaryID := uuid.New()

	active := hive.New(userID, apiaryID, "Active", "")
	if err := repo.Create(ctx, active); err != nil {
		t.Fatalf("create active: %v", err)
	}
	// Soft-deleted before this feature shipped hard deletes: ListByApiary
	// must still find it, since it drives DeleteByApiary's cascade, which
	// needs to finish purging leftovers even for hives already
	// soft-deleted under the old behavior.
	const softDelete = `UPDATE hives SET deleted_at = now() WHERE id = $1`
	softDeleted := hive.New(userID, apiaryID, "Already gone", "")
	if err := repo.Create(ctx, softDeleted); err != nil {
		t.Fatalf("create soft-deleted: %v", err)
	}
	if _, err := tx.Exec(ctx, softDelete, softDeleted.ID); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	// A hive under a different apiary must not show up.
	if err := repo.Create(ctx, hive.New(userID, otherApiaryID, "Elsewhere", "")); err != nil {
		t.Fatalf("create elsewhere: %v", err)
	}

	got, err := repo.ListByApiary(ctx, userID, apiaryID)
	if err != nil {
		t.Fatalf("ListByApiary: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListByApiary returned %d hives, want 2", len(got))
	}
	ids := map[uuid.UUID]bool{}
	for _, h := range got {
		ids[h.ID] = true
	}
	if !ids[active.ID] || !ids[softDeleted.ID] {
		t.Errorf("ListByApiary = %v, want to include both %s and %s", ids, active.ID, softDeleted.ID)
	}
}

func TestHiveRepository_ListByApiary_ScopedToUser(t *testing.T) {
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
	apiaryID := uuid.New()

	if err := repo.Create(ctx, hive.New(owner, apiaryID, "Owner's hive", "")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.ListByApiary(ctx, other, apiaryID)
	if err != nil {
		t.Fatalf("ListByApiary by non-owner: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListByApiary by non-owner = %v, want empty", got)
	}
}
