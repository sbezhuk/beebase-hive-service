//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/pagination"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
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

// TestHiveRepository_ImagesRoundTripThroughCreateAndUpdate proves the
// images column - hive-service's own source of truth for attached media,
// rather than a media-service round trip - survives Create, GetByID, and
// Update intact, including a hive with none.
func TestHiveRepository_ImagesRoundTripThroughCreateAndUpdate(t *testing.T) {
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

	withoutImages := hive.New(userID, apiaryID, "No photos yet", "")
	if err := repo.Create(ctx, withoutImages); err != nil {
		t.Fatalf("Create without images: %v", err)
	}
	got, err := repo.GetByID(ctx, userID, withoutImages.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Images) != 0 {
		t.Fatalf("Images = %v, want empty (not null)", got.Images)
	}

	img1, img2 := uuid.New(), uuid.New()
	withImages := hive.New(userID, apiaryID, "Has photos", "")
	withImages.Images = []uuid.UUID{img1, img2}
	if err := repo.Create(ctx, withImages); err != nil {
		t.Fatalf("Create with images: %v", err)
	}
	got, err = repo.GetByID(ctx, userID, withImages.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Images) != 2 {
		t.Fatalf("Images = %v, want [%s, %s]", got.Images, img1, img2)
	}

	got.Images = []uuid.UUID{img1}
	got.UpdatedAt = time.Now().UTC()
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetByID(ctx, userID, withImages.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != img1 {
		t.Fatalf("Images after update = %v, want [%s]", updated.Images, img1)
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

	list, total, err := repo.ListByUser(ctx, userA, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil, nil, false, nil)
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
	first, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 2}, nil, nil, false, nil)
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
	middle, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 2, Limit: 2}, nil, nil, false, nil)
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
	last, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 3, Limit: 2}, nil, nil, false, nil)
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
	beyond, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 10, Limit: 2}, nil, nil, false, nil)
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

	list, total, err := repo.ListByUser(ctx, uuid.New(), pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil, nil, false, nil)
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

	firstRun, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 4}, nil, nil, false, nil)
	if err != nil {
		t.Fatalf("ListByUser run 1: %v", err)
	}
	secondRun, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 4}, nil, nil, false, nil)
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

func TestHiveRepository_ListByUser_SortOrder(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()

	base := time.Now().UTC()
	names := []string{"Oldest", "Middle", "Newest"}
	for i, name := range names {
		h := hive.New(userID, uuid.New(), name, "")
		h.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		h.UpdatedAt = h.CreatedAt
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	asc := "asc"
	ascending, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil, &asc, false, nil)
	if err != nil {
		t.Fatalf("ListByUser asc: %v", err)
	}
	if got := hiveNamesOf(ascending); !equalHiveStrings(got, []string{"Oldest", "Middle", "Newest"}) {
		t.Fatalf("ascending order = %v, want [Oldest Middle Newest]", got)
	}

	desc := "desc"
	descending, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil, &desc, false, nil)
	if err != nil {
		t.Fatalf("ListByUser desc: %v", err)
	}
	if got := hiveNamesOf(descending); !equalHiveStrings(got, []string{"Newest", "Middle", "Oldest"}) {
		t.Fatalf("descending order = %v, want [Newest Middle Oldest]", got)
	}
}

func TestHiveRepository_ListByApiary_SortOrder(t *testing.T) {
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

	base := time.Now().UTC()
	names := []string{"Oldest", "Middle", "Newest"}
	for i, name := range names {
		h := hive.New(userID, apiaryID, name, "")
		h.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		h.UpdatedAt = h.CreatedAt
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	desc := "desc"
	descending, _, err := repo.ListByApiary(ctx, userID, apiaryID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil, &desc, false, nil)
	if err != nil {
		t.Fatalf("ListByApiary desc: %v", err)
	}
	if got := hiveNamesOf(descending); !equalHiveStrings(got, []string{"Newest", "Middle", "Oldest"}) {
		t.Fatalf("descending order = %v, want [Newest Middle Oldest]", got)
	}
}

func hiveNamesOf(hives []*hive.Hive) []string {
	names := make([]string, len(hives))
	for i, h := range hives {
		names[i] = h.Name
	}
	return names
}

func equalHiveStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

func TestHiveRepository_ListAllByApiary_IncludesAlreadySoftDeleted(t *testing.T) {
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
	// Soft-deleted before this feature shipped hard deletes: ListAllByApiary
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

	got, err := repo.ListAllByApiary(ctx, userID, apiaryID)
	if err != nil {
		t.Fatalf("ListAllByApiary: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListAllByApiary returned %d hives, want 2", len(got))
	}
	ids := map[uuid.UUID]bool{}
	for _, h := range got {
		ids[h.ID] = true
	}
	if !ids[active.ID] || !ids[softDeleted.ID] {
		t.Errorf("ListAllByApiary = %v, want to include both %s and %s", ids, active.ID, softDeleted.ID)
	}
}

func TestHiveRepository_ListAllByApiary_ScopedToUser(t *testing.T) {
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

	got, err := repo.ListAllByApiary(ctx, other, apiaryID)
	if err != nil {
		t.Fatalf("ListAllByApiary by non-owner: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListAllByApiary by non-owner = %v, want empty", got)
	}
}

func TestHiveRepository_ListByApiary_OnlyOwnHivesForThatApiaryExcludingDeleted(t *testing.T) {
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
	apiaryA1 := uuid.New()
	apiaryA2 := uuid.New()

	for _, name := range []string{"A1-1", "A1-2"} {
		if err := repo.Create(ctx, hive.New(userA, apiaryA1, name, "")); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	// Other apiary for userA
	if err := repo.Create(ctx, hive.New(userA, apiaryA2, "A2-1", "")); err != nil {
		t.Fatalf("create A2-1: %v", err)
	}
	// UserB hive in apiaryA1
	if err := repo.Create(ctx, hive.New(userB, apiaryA1, "B1-1", "")); err != nil {
		t.Fatalf("create B1-1: %v", err)
	}
	// Soft deleted hive in apiaryA1 for userA
	softDeleted := hive.New(userA, apiaryA1, "Deleted", "")
	if err := repo.Create(ctx, softDeleted); err != nil {
		t.Fatalf("create soft deleted: %v", err)
	}
	const softDelete = `UPDATE hives SET deleted_at = now() WHERE id = $1`
	if _, err := tx.Exec(ctx, softDelete, softDeleted.ID); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	list, total, err := repo.ListByApiary(ctx, userA, apiaryA1, pagination.Params{Page: 1, Limit: 10}, nil, nil, false, nil)
	if err != nil {
		t.Fatalf("ListByApiary: %v", err)
	}
	if total != 2 {
		t.Fatalf("ListByApiary total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Fatalf("ListByApiary len = %d, want 2", len(list))
	}
	for _, h := range list {
		if h.UserID != userA || h.ApiaryID != apiaryA1 {
			t.Errorf("ListByApiary leaked hive %s", h.ID)
		}
	}
}

func TestHiveRepository_ListByApiary_PaginationAndSearch(t *testing.T) {
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

	for i := 0; i < 5; i++ {
		h := hive.New(userID, apiaryID, fmt.Sprintf("Hive %d", i), "Colony notes")
		if i < 3 {
			h.Notes = "Queen marked yellow"
		}
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	// Pagination: page 1 limit 2
	p1, total, err := repo.ListByApiary(ctx, userID, apiaryID, pagination.Params{Page: 1, Limit: 2}, nil, nil, false, nil)
	if err != nil {
		t.Fatalf("ListByApiary p1: %v", err)
	}
	if total != 5 || len(p1) != 2 {
		t.Fatalf("p1: total = %d (want 5), len = %d (want 2)", total, len(p1))
	}

	// Search: "queen"
	search := "queen"
	res, total, err := repo.ListByApiary(ctx, userID, apiaryID, pagination.Params{Page: 1, Limit: 10}, &search, nil, false, nil)
	if err != nil {
		t.Fatalf("ListByApiary search: %v", err)
	}
	if total != 3 || len(res) != 3 {
		t.Fatalf("search: total = %d (want 3), len = %d (want 3)", total, len(res))
	}
}

func TestHiveRepository_ListByUser_NeedsInspectionOnly(t *testing.T) {
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

	needsIt := hive.New(userID, apiaryID, "Needs inspection", "")
	fine := hive.New(userID, apiaryID, "Fine", "")
	for _, h := range []*hive.Hive{needsIt, fine} {
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create %s: %v", h.Name, err)
		}
	}

	// The "needs inspection" set is computed by the application layer
	// (this repository has no notion of inspections); here it's just
	// needsIt.ID.
	list, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil, nil, true, []uuid.UUID{needsIt.ID})
	if err != nil {
		t.Fatalf("ListByUser needs_inspection: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if len(list) != 1 || list[0].ID != needsIt.ID {
		t.Fatalf("list = %+v, want only %s", list, needsIt.ID)
	}
}

func TestHiveRepository_ListByUser_NeedsInspectionOnly_EmptySetMatchesNothing(t *testing.T) {
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

	h := hive.New(userID, apiaryID, "Fine", "")
	if err := repo.Create(ctx, h); err != nil {
		t.Fatalf("create: %v", err)
	}

	// An empty needs-inspection set (every hive is currently fine) must
	// yield zero rows, not "no filter" (which would wrongly return h).
	list, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil, nil, true, nil)
	if err != nil {
		t.Fatalf("ListByUser needs_inspection with empty set: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("total = %d, len = %d, want 0/0", total, len(list))
	}
}

func TestHiveRepository_ListIDsByUser_ExcludesDeletedAndOtherUsers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewHiveRepository(tx)
	userID := uuid.New()
	otherUser := uuid.New()
	apiaryID := uuid.New()

	kept := hive.New(userID, apiaryID, "Kept", "")
	deleted := hive.New(userID, apiaryID, "Deleted", "")
	other := hive.New(otherUser, apiaryID, "Someone else's", "")
	for _, h := range []*hive.Hive{kept, deleted, other} {
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create %s: %v", h.Name, err)
		}
	}
	if err := repo.HardDelete(ctx, userID, deleted.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	ids, err := repo.ListIDsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListIDsByUser: %v", err)
	}
	if len(ids) != 1 || ids[0] != kept.ID {
		t.Fatalf("ids = %v, want only %s", ids, kept.ID)
	}
}

func TestHiveRepository_DistinctApiaryIDsWithHives(t *testing.T) {
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
	apiaryEmpty := uuid.New()

	for _, h := range []*hive.Hive{
		hive.New(userID, apiaryA, "A1", ""),
		hive.New(userID, apiaryA, "A2", ""),
		hive.New(userID, apiaryB, "B1", ""),
	} {
		if err := repo.Create(ctx, h); err != nil {
			t.Fatalf("create %s: %v", h.Name, err)
		}
	}

	ids, err := repo.DistinctApiaryIDsWithHives(ctx, userID)
	if err != nil {
		t.Fatalf("DistinctApiaryIDsWithHives: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want 2 (apiaryA once, apiaryB once)", ids)
	}
	got := map[uuid.UUID]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got[apiaryA] || !got[apiaryB] {
		t.Errorf("ids = %v, want apiaryA and apiaryB", ids)
	}
	if got[apiaryEmpty] {
		t.Error("apiaryEmpty has no hives, should not appear")
	}
}

// TestHiveRepository_HardDelete_CascadesHiveQueens proves the database-level
// ON DELETE CASCADE on hive_queens(hive_id) guarantees that hard-deleting a hive
// physically removes all associated queen history (current and historical),
// leaving zero orphaned records, while isolated hives/queens remain intact.
func TestHiveRepository_HardDelete_CascadesHiveQueens(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	userID := uuid.New()
	apiaryID := uuid.New()

	// Hive A with 3 queens: 2 historical, 1 current
	hiveA := hive.New(userID, apiaryID, "Hive A", "")
	if err := hiveRepo.Create(ctx, hiveA); err != nil {
		t.Fatalf("create hiveA: %v", err)
	}

	t1Intro := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	qA1 := domainqueen.New(hiveA.ID, 2025, &t1Intro, t1Intro, nil, nil, "Historical 2025")

	t2Intro := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	qA2 := domainqueen.New(hiveA.ID, 2026, &t2Intro, t2Intro, nil, nil, "Historical 2026")

	t3Intro := time.Date(2027, 4, 1, 0, 0, 0, 0, time.UTC)
	qA3 := domainqueen.New(hiveA.ID, 2027, &t3Intro, t3Intro, nil, nil, "Current 2027")

	for _, q := range []*domainqueen.Queen{qA1, qA2, qA3} {
		if err := queenRepo.InsertInChain(ctx, q, nil); err != nil {
			t.Fatalf("create queen %s: %v", q.Notes, err)
		}
	}

	// Hive B (isolation test under same user/apiary) with 1 current queen
	hiveB := hive.New(userID, apiaryID, "Hive B", "")
	if err := hiveRepo.Create(ctx, hiveB); err != nil {
		t.Fatalf("create hiveB: %v", err)
	}
	qB1 := domainqueen.New(hiveB.ID, 2027, &t3Intro, t3Intro, nil, nil, "Hive B Queen")
	if err := queenRepo.InsertInChain(ctx, qB1, nil); err != nil {
		t.Fatalf("create qB1: %v", err)
	}

	// Hard-delete Hive A
	if err := hiveRepo.HardDelete(ctx, userID, hiveA.ID); err != nil {
		t.Fatalf("HardDelete hiveA: %v", err)
	}

	// Invariant check: SELECT COUNT(*) FROM hive_queens WHERE hive_id = hiveA.ID == 0
	var countA int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM hive_queens WHERE hive_id = $1", hiveA.ID).Scan(&countA); err != nil {
		t.Fatalf("count hiveA queens: %v", err)
	}
	if countA != 0 {
		t.Fatalf("expected 0 hive_queens for deleted hiveA, got %d", countA)
	}

	// Verify domain query returns ErrNotFound
	if _, err := queenRepo.GetByID(ctx, hiveA.ID, qA3.ID); !errors.Is(err, domainqueen.ErrNotFound) {
		t.Fatalf("GetByID for deleted hive's queen: got %v, want ErrNotFound", err)
	}

	// Isolation check: Hive B and its queen are completely intact
	var countB int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM hive_queens WHERE hive_id = $1", hiveB.ID).Scan(&countB); err != nil {
		t.Fatalf("count hiveB queens: %v", err)
	}
	if countB != 1 {
		t.Fatalf("hiveB queens modified unexpectedly: got %d, want 1", countB)
	}
	if _, err := queenRepo.GetByID(ctx, hiveB.ID, qB1.ID); err != nil {
		t.Fatalf("hiveB queen not found: %v", err)
	}
}

// TestHiveRepository_DeleteAllByUserHard_CascadesHiveQueens proves that account-level
// hard delete of hives cascades cleanly to all hive_queens for that user across all their hives,
// while leaving unrelated users' queen data intact.
func TestHiveRepository_DeleteAllByUserHard_CascadesHiveQueens(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	user1 := uuid.New()
	user2 := uuid.New()

	apiary1 := uuid.New()
	apiary2 := uuid.New()

	// User 1 hives & queens
	h1 := hive.New(user1, apiary1, "User1 Hive1", "")
	h2 := hive.New(user1, apiary1, "User1 Hive2", "")
	for _, h := range []*hive.Hive{h1, h2} {
		if err := hiveRepo.Create(ctx, h); err != nil {
			t.Fatalf("create h: %v", err)
		}
	}
	tIntro := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	q1 := domainqueen.New(h1.ID, 2026, &tIntro, tIntro, nil, nil, "U1H1 Queen")
	q2 := domainqueen.New(h2.ID, 2026, &tIntro, tIntro, nil, nil, "U1H2 Queen")
	for _, q := range []*domainqueen.Queen{q1, q2} {
		if err := queenRepo.InsertInChain(ctx, q, nil); err != nil {
			t.Fatalf("create q: %v", err)
		}
	}

	// User 2 hive & queen (unrelated user)
	h3 := hive.New(user2, apiary2, "User2 Hive", "")
	if err := hiveRepo.Create(ctx, h3); err != nil {
		t.Fatalf("create h3: %v", err)
	}
	q3 := domainqueen.New(h3.ID, 2026, &tIntro, tIntro, nil, nil, "U2 Queen")
	if err := queenRepo.InsertInChain(ctx, q3, nil); err != nil {
		t.Fatalf("create q3: %v", err)
	}

	// Execute account cleanup for user1
	if err := hiveRepo.DeleteAllByUserHard(ctx, user1); err != nil {
		t.Fatalf("DeleteAllByUserHard: %v", err)
	}

	// Check all User 1 hives are deleted
	var countHivesU1 int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM hives WHERE user_id = $1", user1).Scan(&countHivesU1); err != nil {
		t.Fatalf("count user1 hives: %v", err)
	}
	if countHivesU1 != 0 {
		t.Fatalf("user1 hives count = %d, want 0", countHivesU1)
	}

	// Check all User 1 queens are deleted
	var countQueensU1 int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM hive_queens WHERE hive_id IN ($1, $2)", h1.ID, h2.ID).Scan(&countQueensU1); err != nil {
		t.Fatalf("count user1 queens: %v", err)
	}
	if countQueensU1 != 0 {
		t.Fatalf("user1 queens count = %d, want 0", countQueensU1)
	}

	// Check User 2 hive and queen are untouched
	var countHivesU2, countQueensU2 int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM hives WHERE user_id = $1", user2).Scan(&countHivesU2); err != nil {
		t.Fatalf("count user2 hives: %v", err)
	}
	if countHivesU2 != 1 {
		t.Fatalf("user2 hives count = %d, want 1", countHivesU2)
	}
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM hive_queens WHERE hive_id = $1", h3.ID).Scan(&countQueensU2); err != nil {
		t.Fatalf("count user2 queens: %v", err)
	}
	if countQueensU2 != 1 {
		t.Fatalf("user2 queens count = %d, want 1", countQueensU2)
	}
}

func TestQueenRepository_Integration_ChainLifecycleAndWindowQueries(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	userID := uuid.New()
	apiaryID := uuid.New()

	h := hive.New(userID, apiaryID, "Integration Hive", "")
	if err := hiveRepo.Create(ctx, h); err != nil {
		t.Fatalf("create hive: %v", err)
	}

	// 1. First queen with reason is rejected
	tA := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	reasonBad := domainqueen.ReasonLowEggLaying
	qA := domainqueen.New(h.ID, 2025, &tA, tA, nil, &reasonBad, "Queen A")
	if err := queenRepo.InsertInChain(ctx, qA, &reasonBad); !errors.Is(err, domainqueen.ErrReplacementReasonNotAllowed) {
		t.Fatalf("expected ErrReplacementReasonNotAllowed for first queen with reason, got: %v", err)
	}

	// 2. First queen without reason succeeds
	qA = domainqueen.New(h.ID, 2025, &tA, tA, nil, nil, "Queen A")
	if err := queenRepo.InsertInChain(ctx, qA, nil); err != nil {
		t.Fatalf("insert Q_A: %v", err)
	}

	// Verify Q_A is current and incoming reason is nil
	cur, err := queenRepo.GetCurrentByHiveID(ctx, h.ID)
	if err != nil {
		t.Fatalf("get current: %v", err)
	}
	if cur.ID != qA.ID || cur.RemovedAt != nil || cur.ReplacementReason != nil {
		t.Fatalf("unexpected Q_A state: %+v", cur)
	}

	// 3. Append Q_B with reason LOW_EGG_LAYING
	tB := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	reasonAB := domainqueen.ReasonLowEggLaying
	qB := domainqueen.New(h.ID, 2026, &tB, tB, nil, &reasonAB, "Queen B")
	if err := queenRepo.InsertInChain(ctx, qB, &reasonAB); err != nil {
		t.Fatalf("insert Q_B: %v", err)
	}

	// Verify Q_B incoming reason via GetCurrentByHiveID (CTE window query)
	cur, err = queenRepo.GetCurrentByHiveID(ctx, h.ID)
	if err != nil {
		t.Fatalf("get current: %v", err)
	}
	if cur.ID != qB.ID {
		t.Fatalf("expected current queen to be Q_B, got: %v", cur.ID)
	}
	if cur.ReplacementReason == nil || *cur.ReplacementReason != domainqueen.ReasonLowEggLaying {
		t.Fatalf("expected Q_B incoming reason LOW_EGG_LAYING via window LAG, got: %v", cur.ReplacementReason)
	}

	// Verify Q_B incoming reason via GetByID
	gotB, err := queenRepo.GetByID(ctx, h.ID, qB.ID)
	if err != nil {
		t.Fatalf("get Q_B by id: %v", err)
	}
	if gotB.ReplacementReason == nil || *gotB.ReplacementReason != domainqueen.ReasonLowEggLaying {
		t.Fatalf("expected Q_B GetByID incoming reason LOW_EGG_LAYING, got: %v", gotB.ReplacementReason)
	}

	// Verify Q_A via GetByID: removed_at = tB, physical replacement_reason = LOW_EGG_LAYING, but incoming reason = nil
	gotA, err := queenRepo.GetByID(ctx, h.ID, qA.ID)
	if err != nil {
		t.Fatalf("get Q_A by id: %v", err)
	}
	if gotA.RemovedAt == nil || !gotA.RemovedAt.Equal(tB) {
		t.Fatalf("expected Q_A removedAt = tB, got: %v", gotA.RemovedAt)
	}
	if gotA.ReplacementReason != nil {
		t.Fatalf("expected Q_A incoming reason = nil (oldest queen), got: %v", gotA.ReplacementReason)
	}

	// 4. Append Q_C with reason AGING_AND_WEAR
	tC := time.Date(2027, 4, 1, 0, 0, 0, 0, time.UTC)
	reasonBC := domainqueen.ReasonAgingAndWear
	qC := domainqueen.New(h.ID, 2027, &tC, tC, nil, &reasonBC, "Queen C")
	if err := queenRepo.InsertInChain(ctx, qC, &reasonBC); err != nil {
		t.Fatalf("insert Q_C: %v", err)
	}

	// 5. Update current queen Q_C incoming reason: PUT Q_C reason DISEASE_OR_POOR_QUALITY
	reasonBCUpdated := domainqueen.ReasonDiseaseOrPoorQuality
	updatedC, err := queenRepo.UpdateInChain(ctx, h.ID, qC.ID, 2027, nil, tC, &reasonBCUpdated, true, "Updated C")
	if err != nil {
		t.Fatalf("update Q_C reason: %v", err)
	}
	if updatedC.ReplacementReason == nil || *updatedC.ReplacementReason != domainqueen.ReasonDiseaseOrPoorQuality {
		t.Fatalf("expected updated incoming reason DISEASE_OR_POOR_QUALITY on Q_C, got: %v", updatedC.ReplacementReason)
	}

	// Check that physical Q_C has replacement_reason = NULL in database
	var physicalCReason *string
	if err := tx.QueryRow(ctx, "SELECT replacement_reason FROM hive_queens WHERE id = $1", qC.ID).Scan(&physicalCReason); err != nil {
		t.Fatalf("query physical Q_C: %v", err)
	}
	if physicalCReason != nil {
		t.Fatalf("physical replacement_reason on active queen Q_C must be NULL, got: %v", *physicalCReason)
	}

	// Check that predecessor Q_B has physical replacement_reason = DISEASE_OR_POOR_QUALITY in database
	var physicalBReason *string
	if err := tx.QueryRow(ctx, "SELECT replacement_reason FROM hive_queens WHERE id = $1", qB.ID).Scan(&physicalBReason); err != nil {
		t.Fatalf("query physical Q_B: %v", err)
	}
	if physicalBReason == nil || *physicalBReason != string(domainqueen.ReasonDiseaseOrPoorQuality) {
		t.Fatalf("physical replacement_reason on predecessor Q_B should be DISEASE_OR_POOR_QUALITY, got: %v", physicalBReason)
	}

	// 6. DeleteLatest: DELETE Q_C
	if err := queenRepo.DeleteLatest(ctx, h.ID, qC.ID); err != nil {
		t.Fatalf("delete Q_C: %v", err)
	}

	// Q_B is rolled back to current: removed_at = NULL, physical replacement_reason = NULL
	cur, err = queenRepo.GetCurrentByHiveID(ctx, h.ID)
	if err != nil {
		t.Fatalf("get current after rollback: %v", err)
	}
	if cur.ID != qB.ID || cur.RemovedAt != nil {
		t.Fatalf("expected Q_B to be active, got: %+v", cur)
	}
	// Q_B incoming reason (from Q_A) is preserved!
	if cur.ReplacementReason == nil || *cur.ReplacementReason != domainqueen.ReasonLowEggLaying {
		t.Fatalf("expected Q_B incoming reason LOW_EGG_LAYING to be preserved, got: %v", cur.ReplacementReason)
	}

	// Physical Q_B outgoing reason is now NULL
	if err := tx.QueryRow(ctx, "SELECT replacement_reason FROM hive_queens WHERE id = $1", qB.ID).Scan(&physicalBReason); err != nil {
		t.Fatalf("query physical Q_B: %v", err)
	}
	if physicalBReason != nil {
		t.Fatalf("physical replacement_reason on rolled back Q_B must be NULL, got: %v", *physicalBReason)
	}
}

func TestQueenRepository_Integration_InsertRetroactiveAndBeforeOldest(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	userID := uuid.New()
	apiaryID := uuid.New()
	h := hive.New(userID, apiaryID, "Retro Hive", "")
	if err := hiveRepo.Create(ctx, h); err != nil {
		t.Fatalf("create hive: %v", err)
	}

	// 1. Initial chain: A (2025) -> C (2027) with reason X on A
	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	qA := domainqueen.New(h.ID, 2025, nil, tA, nil, nil, "A")
	if err := queenRepo.InsertInChain(ctx, qA, nil); err != nil {
		t.Fatalf("insert A: %v", err)
	}

	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonAC := domainqueen.ReasonNaturalSupersedure
	qC := domainqueen.New(h.ID, 2027, nil, tC, nil, &reasonAC, "C")
	if err := queenRepo.InsertInChain(ctx, qC, &reasonAC); err != nil {
		t.Fatalf("insert C: %v", err)
	}

	// 2. Retroactive insertion of B (2026) between A and C with reason Y
	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonAB := domainqueen.ReasonLowEggLaying
	qB := domainqueen.New(h.ID, 2026, nil, tB, nil, &reasonAB, "B")
	if err := queenRepo.InsertInChain(ctx, qB, &reasonAB); err != nil {
		t.Fatalf("insert B between A and C: %v", err)
	}

	// In database:
	// A.removed_at = tB, A.replacement_reason = LOW_EGG_LAYING
	// B.removed_at = tC, B.replacement_reason = NULL (old reason is NOT transferred)
	// C.removed_at = NULL, C.replacement_reason = NULL
	var aRemovedAt *time.Time
	var aReason, bReason, cReason *string
	var bRemovedAt, cRemovedAt *time.Time

	if err := tx.QueryRow(ctx, "SELECT removed_at, replacement_reason FROM hive_queens WHERE id = $1", qA.ID).Scan(&aRemovedAt, &aReason); err != nil {
		t.Fatalf("query A: %v", err)
	}
	if aRemovedAt == nil || !aRemovedAt.Equal(tB) || aReason == nil || *aReason != string(domainqueen.ReasonLowEggLaying) {
		t.Fatalf("unexpected A row: removedAt=%v, reason=%v", aRemovedAt, aReason)
	}

	if err := tx.QueryRow(ctx, "SELECT removed_at, replacement_reason FROM hive_queens WHERE id = $1", qB.ID).Scan(&bRemovedAt, &bReason); err != nil {
		t.Fatalf("query B: %v", err)
	}
	if bRemovedAt == nil || !bRemovedAt.Equal(tC) || bReason != nil {
		t.Fatalf("unexpected B row: removedAt=%v, reason=%v (must be null)", bRemovedAt, bReason)
	}

	if err := tx.QueryRow(ctx, "SELECT removed_at, replacement_reason FROM hive_queens WHERE id = $1", qC.ID).Scan(&cRemovedAt, &cReason); err != nil {
		t.Fatalf("query C: %v", err)
	}
	if cRemovedAt != nil || cReason != nil {
		t.Fatalf("unexpected C row: removedAt=%v, reason=%v", cRemovedAt, cReason)
	}

	// 3. Insert before oldest: A0 (2024)
	tA0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonA0 := domainqueen.ReasonDiseaseOrPoorQuality
	qA0 := domainqueen.New(h.ID, 2024, nil, tA0, nil, &reasonA0, "A0")
	// Supplying reason must fail
	if err := queenRepo.InsertInChain(ctx, qA0, &reasonA0); !errors.Is(err, domainqueen.ErrReplacementReasonNotAllowed) {
		t.Fatalf("expected ErrReplacementReasonNotAllowed when inserting before oldest with reason, got: %v", err)
	}

	// Supplying without reason succeeds: A0.removed_at = tA, A0.replacement_reason = NULL
	qA0.ReplacementReason = nil
	if err := queenRepo.InsertInChain(ctx, qA0, nil); err != nil {
		t.Fatalf("insert A0 before oldest without reason: %v", err)
	}
	var a0RemovedAt *time.Time
	var a0Reason *string
	if err := tx.QueryRow(ctx, "SELECT removed_at, replacement_reason FROM hive_queens WHERE id = $1", qA0.ID).Scan(&a0RemovedAt, &a0Reason); err != nil {
		t.Fatalf("query A0: %v", err)
	}
	if a0RemovedAt == nil || !a0RemovedAt.Equal(tA) || a0Reason != nil {
		t.Fatalf("unexpected A0 row: removedAt=%v, reason=%v", a0RemovedAt, a0Reason)
	}
}

func TestQueenRepository_Integration_UpdateBoundsAndAtomicity(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	userID := uuid.New()
	apiaryID := uuid.New()
	h := hive.New(userID, apiaryID, "Bounds Hive", "")
	if err := hiveRepo.Create(ctx, h); err != nil {
		t.Fatalf("create hive: %v", err)
	}

	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	qA := domainqueen.New(h.ID, 2025, nil, tA, nil, nil, "A")
	_ = queenRepo.InsertInChain(ctx, qA, nil)

	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonAB := domainqueen.ReasonLowEggLaying
	qB := domainqueen.New(h.ID, 2026, nil, tB, nil, &reasonAB, "B")
	_ = queenRepo.InsertInChain(ctx, qB, &reasonAB)

	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonBC := domainqueen.ReasonAgingAndWear
	qC := domainqueen.New(h.ID, 2027, nil, tC, nil, &reasonBC, "C")
	_ = queenRepo.InsertInChain(ctx, qC, &reasonBC)

	// 1. Invalid update: B.introducedAt == A.introducedAt -> ErrTimelineInvalid
	if _, err := queenRepo.UpdateInChain(ctx, h.ID, qB.ID, 2026, nil, tA, nil, false, "Invalid"); !errors.Is(err, domainqueen.ErrTimelineInvalid) {
		t.Fatalf("expected ErrTimelineInvalid for B == A, got: %v", err)
	}

	// 2. Invalid update: B.introducedAt == C.introducedAt -> ErrTimelineInvalid
	if _, err := queenRepo.UpdateInChain(ctx, h.ID, qB.ID, 2026, nil, tC, nil, false, "Invalid"); !errors.Is(err, domainqueen.ErrTimelineInvalid) {
		t.Fatalf("expected ErrTimelineInvalid for B == C, got: %v", err)
	}

	// 3. Oldest queen A with non-null reason -> ErrReplacementReasonNotAllowed
	reasonBad := domainqueen.ReasonDiseaseOrPoorQuality
	if _, err := queenRepo.UpdateInChain(ctx, h.ID, qA.ID, 2025, nil, tA, &reasonBad, true, "Invalid"); !errors.Is(err, domainqueen.ErrReplacementReasonNotAllowed) {
		t.Fatalf("expected ErrReplacementReasonNotAllowed for oldest queen with reason, got: %v", err)
	}

	// 4. Valid update: B.introducedAt = 2026-06-01 + reason = BREED_CHANGE_OR_AGGRESSIVENESS
	newTB := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	newReason := domainqueen.ReasonBreedChangeOrAggressiveness
	updatedB, err := queenRepo.UpdateInChain(ctx, h.ID, qB.ID, 2026, nil, newTB, &newReason, true, "Valid update")
	if err != nil {
		t.Fatalf("valid update failed: %v", err)
	}
	if updatedB.ReplacementReason == nil || *updatedB.ReplacementReason != newReason {
		t.Fatalf("expected returned incoming reason %s, got: %v", newReason, updatedB.ReplacementReason)
	}

	// Verify predecessor A has removed_at = newTB and replacement_reason = newReason
	var aRemovedAt *time.Time
	var aReason *string
	if err := tx.QueryRow(ctx, "SELECT removed_at, replacement_reason FROM hive_queens WHERE id = $1", qA.ID).Scan(&aRemovedAt, &aReason); err != nil {
		t.Fatalf("query A: %v", err)
	}
	if aRemovedAt == nil || !aRemovedAt.Equal(newTB) {
		t.Fatalf("expected A removedAt = %v, got %v", newTB, aRemovedAt)
	}
	if aReason == nil || *aReason != string(newReason) {
		t.Fatalf("expected A reason = %s, got %v", newReason, aReason)
	}
}

func TestQueenRepository_Integration_PostgresConstraintsAndRollback(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	userID := uuid.New()
	apiaryID := uuid.New()
	h := hive.New(userID, apiaryID, "Constraints Hive", "")
	if err := hiveRepo.Create(ctx, h); err != nil {
		t.Fatalf("create hive: %v", err)
	}

	tIntro := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q1 := domainqueen.New(h.ID, 2026, nil, tIntro, nil, nil, "Q1")
	if err := queenRepo.InsertInChain(ctx, q1, nil); err != nil {
		t.Fatalf("insert Q1: %v", err)
	}

	// 1. Duplicate (hive_id, introduced_at) constraint via InsertInChain
	qDup := domainqueen.New(h.ID, 2026, nil, tIntro, nil, nil, "Q-Dup")
	if err := queenRepo.InsertInChain(ctx, qDup, nil); !errors.Is(err, domainqueen.ErrDuplicateIntroducedAt) {
		t.Fatalf("expected ErrDuplicateIntroducedAt, got: %v", err)
	}

	// 2. Raw SQL check: idx_hive_queens_one_current_per_hive (two active queens)
	secondID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO hive_queens (id, hive_id, year, introduced_at, removed_at, notes)
		VALUES ($1, $2, 2027, '2027-01-01T00:00:00Z', NULL, 'Second Active')
	`, secondID, h.ID)
	if err == nil {
		t.Fatalf("expected unique constraint violation on idx_hive_queens_one_current_per_hive")
	}

	// 3. Raw SQL check: chk_hive_queens_active_has_no_replacement_reason
	// (active queen with removed_at IS NULL cannot have non-null replacement_reason)
	thirdID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO hive_queens (id, hive_id, year, introduced_at, removed_at, replacement_reason, notes)
		VALUES ($1, $2, 2028, '2028-01-01T00:00:00Z', NULL, 'LOW_EGG_LAYING', 'Invalid Active Reason')
	`, thirdID, h.ID)
	if err == nil {
		t.Fatalf("expected check constraint violation on chk_hive_queens_active_has_no_replacement_reason")
	}

	// 4. Raw SQL check: chk_hive_queens_replacement_reason_valid
	fourthID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO hive_queens (id, hive_id, year, introduced_at, removed_at, replacement_reason, notes)
		VALUES ($1, $2, 2029, '2029-01-01T00:00:00Z', '2029-06-01T00:00:00Z', 'INVALID_ENUM_STRING', 'Invalid Reason')
	`, fourthID, h.ID)
	if err == nil {
		t.Fatalf("expected check constraint violation on chk_hive_queens_replacement_reason_valid")
	}

	// 5. Raw SQL check: FK constraint to non-existent hive
	nonExistentHive := uuid.New()
	fifthID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO hive_queens (id, hive_id, year, introduced_at, removed_at, notes)
		VALUES ($1, $2, 2030, '2030-01-01T00:00:00Z', NULL, 'Orphan')
	`, fifthID, nonExistentHive)
	if err == nil {
		t.Fatalf("expected foreign key constraint violation on hive_queens_hive_id_fkey")
	}
}

func TestQueenRepository_Integration_DeleteLatest_NonLatestRejectedAndFullRollback(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	userID := uuid.New()
	apiaryID := uuid.New()
	h := hive.New(userID, apiaryID, "Delete Hive", "")
	if err := hiveRepo.Create(ctx, h); err != nil {
		t.Fatalf("create hive: %v", err)
	}

	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	qA := domainqueen.New(h.ID, 2025, nil, tA, nil, nil, "A")
	_ = queenRepo.InsertInChain(ctx, qA, nil)

	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonAB := domainqueen.ReasonLowEggLaying
	qB := domainqueen.New(h.ID, 2026, nil, tB, nil, &reasonAB, "B")
	_ = queenRepo.InsertInChain(ctx, qB, &reasonAB)

	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonBC := domainqueen.ReasonAgingAndWear
	qC := domainqueen.New(h.ID, 2027, nil, tC, nil, &reasonBC, "C")
	_ = queenRepo.InsertInChain(ctx, qC, &reasonBC)

	// 1. Attempt non-latest delete: DELETE A -> ErrQueenNotLatest
	if err := queenRepo.DeleteLatest(ctx, h.ID, qA.ID); !errors.Is(err, domainqueen.ErrQueenNotLatest) {
		t.Fatalf("expected ErrQueenNotLatest for deleting A, got: %v", err)
	}

	// 2. Attempt non-latest delete: DELETE B -> ErrQueenNotLatest
	if err := queenRepo.DeleteLatest(ctx, h.ID, qB.ID); !errors.Is(err, domainqueen.ErrQueenNotLatest) {
		t.Fatalf("expected ErrQueenNotLatest for deleting B, got: %v", err)
	}

	// 3. DELETE C -> B rolls back to current, A's reason preserved
	if err := queenRepo.DeleteLatest(ctx, h.ID, qC.ID); err != nil {
		t.Fatalf("delete C: %v", err)
	}
	curB, err := queenRepo.GetCurrentByHiveID(ctx, h.ID)
	if err != nil {
		t.Fatalf("get current B: %v", err)
	}
	if curB.ID != qB.ID || curB.RemovedAt != nil || curB.ReplacementReason == nil || *curB.ReplacementReason != reasonAB {
		t.Fatalf("unexpected state for B after C deleted: %+v", curB)
	}

	// 4. DELETE B -> A rolls back to current, A's reason cleared to NULL
	if err := queenRepo.DeleteLatest(ctx, h.ID, qB.ID); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	curA, err := queenRepo.GetCurrentByHiveID(ctx, h.ID)
	if err != nil {
		t.Fatalf("get current A: %v", err)
	}
	if curA.ID != qA.ID || curA.RemovedAt != nil || curA.ReplacementReason != nil {
		t.Fatalf("unexpected state for A after B deleted: %+v", curA)
	}

	// 5. DELETE A -> 0 queens in hive
	if err := queenRepo.DeleteLatest(ctx, h.ID, qA.ID); err != nil {
		t.Fatalf("delete A: %v", err)
	}
	if _, err := queenRepo.GetCurrentByHiveID(ctx, h.ID); !errors.Is(err, domainqueen.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty hive, got: %v", err)
	}
}

func TestQueenRepository_Integration_ListHistory_LagOrdering(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	hiveRepo := repopostgres.NewHiveRepository(tx)
	queenRepo := repopostgres.NewQueenRepository(tx)

	userID := uuid.New()
	apiaryID := uuid.New()
	h := hive.New(userID, apiaryID, "History Hive", "")
	if err := hiveRepo.Create(ctx, h); err != nil {
		t.Fatalf("create hive: %v", err)
	}

	tA := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	qA := domainqueen.New(h.ID, 2025, nil, tA, nil, nil, "A")
	_ = queenRepo.InsertInChain(ctx, qA, nil)

	tB := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonAB := domainqueen.ReasonLowEggLaying
	qB := domainqueen.New(h.ID, 2026, nil, tB, nil, &reasonAB, "B")
	_ = queenRepo.InsertInChain(ctx, qB, &reasonAB)

	tC := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	reasonBC := domainqueen.ReasonAgingAndWear
	qC := domainqueen.New(h.ID, 2027, nil, tC, nil, &reasonBC, "C")
	_ = queenRepo.InsertInChain(ctx, qC, &reasonBC)

	list, err := queenRepo.ListHistoryByHiveID(ctx, h.ID)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 queens, got %d", len(list))
	}

	// List ordering is newest first: C, B, A
	if list[0].ID != qC.ID || list[1].ID != qB.ID || list[2].ID != qA.ID {
		t.Fatalf("expected order [C, B, A], got [%v, %v, %v]", list[0].ID, list[1].ID, list[2].ID)
	}

	// Incoming reasons via window LAG():
	// C incoming = reasonBC (from B)
	if list[0].ReplacementReason == nil || *list[0].ReplacementReason != reasonBC {
		t.Fatalf("expected C incoming reason = %s, got: %v", reasonBC, list[0].ReplacementReason)
	}
	// B incoming = reasonAB (from A)
	if list[1].ReplacementReason == nil || *list[1].ReplacementReason != reasonAB {
		t.Fatalf("expected B incoming reason = %s, got: %v", reasonAB, list[1].ReplacementReason)
	}
	// A incoming = nil (oldest)
	if list[2].ReplacementReason != nil {
		t.Fatalf("expected A incoming reason = nil, got: %v", list[2].ReplacementReason)
	}
}

func TestQueenRepository_Integration_ConcurrencyAdvisoryLock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// Advisory locks require non-transactional pool connection to demonstrate cross-transaction concurrency
	queenRepo := repopostgres.NewQueenRepository(pool)
	hiveRepo := repopostgres.NewHiveRepository(pool)

	userID := uuid.New()
	apiaryID := uuid.New()

	h1 := hive.New(userID, apiaryID, "Concurrent Hive 1", "")
	if err := hiveRepo.Create(ctx, h1); err != nil {
		t.Fatalf("create h1: %v", err)
	}
	t.Cleanup(func() {
		_ = hiveRepo.HardDelete(context.Background(), userID, h1.ID)
	})

	h2 := hive.New(userID, apiaryID, "Concurrent Hive 2", "")
	if err := hiveRepo.Create(ctx, h2); err != nil {
		t.Fatalf("create h2: %v", err)
	}
	t.Cleanup(func() {
		_ = hiveRepo.HardDelete(context.Background(), userID, h2.ID)
	})

	// Initial queens
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	q1A := domainqueen.New(h1.ID, 2025, nil, t0, nil, nil, "H1 Initial")
	if err := queenRepo.InsertInChain(ctx, q1A, nil); err != nil {
		t.Fatalf("insert q1A: %v", err)
	}

	q2A := domainqueen.New(h2.ID, 2025, nil, t0, nil, nil, "H2 Initial")
	if err := queenRepo.InsertInChain(ctx, q2A, nil); err != nil {
		t.Fatalf("insert q2A: %v", err)
	}

	// Concurrent mutations across H1 and H2
	done := make(chan error, 4)

	// Op 1 on H1: Append queen 2026
	go func() {
		t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		r := domainqueen.ReasonLowEggLaying
		q := domainqueen.New(h1.ID, 2026, nil, t1, nil, &r, "H1 Q2")
		done <- queenRepo.InsertInChain(context.Background(), q, &r)
	}()

	// Op 2 on H2: Append queen 2026
	go func() {
		t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		r := domainqueen.ReasonNaturalSupersedure
		q := domainqueen.New(h2.ID, 2026, nil, t1, nil, &r, "H2 Q2")
		done <- queenRepo.InsertInChain(context.Background(), q, &r)
	}()

	// Op 3 on H1: Append queen 2027
	go func() {
		time.Sleep(10 * time.Millisecond) // slight offset to simulate real race
		t2 := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
		r := domainqueen.ReasonAgingAndWear
		q := domainqueen.New(h1.ID, 2027, nil, t2, nil, &r, "H1 Q3")
		done <- queenRepo.InsertInChain(context.Background(), q, &r)
	}()

	// Op 4 on H2: Append queen 2027
	go func() {
		time.Sleep(10 * time.Millisecond)
		t2 := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
		r := domainqueen.ReasonBreedChangeOrAggressiveness
		q := domainqueen.New(h2.ID, 2027, nil, t2, nil, &r, "H2 Q3")
		done <- queenRepo.InsertInChain(context.Background(), q, &r)
	}()

	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent operation %d failed: %v", i+1, err)
		}
	}

	// Verify both hives have exactly 3 queens, ordered cleanly
	list1, err := queenRepo.ListHistoryByHiveID(ctx, h1.ID)
	if err != nil || len(list1) != 3 {
		t.Fatalf("H1 expected 3 queens, got %d (err: %v)", len(list1), err)
	}
	list2, err := queenRepo.ListHistoryByHiveID(ctx, h2.ID)
	if err != nil || len(list2) != 3 {
		t.Fatalf("H2 expected 3 queens, got %d (err: %v)", len(list2), err)
	}
}
