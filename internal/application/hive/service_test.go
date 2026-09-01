package hive_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/pagination"
	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// --- in-memory fake repository ---

type fakeRepo struct {
	mu   sync.Mutex
	byID map[uuid.UUID]*hive.Hive
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byID: map[uuid.UUID]*hive.Hive{}}
}

func (f *fakeRepo) Create(_ context.Context, h *hive.Hive) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *h
	f.byID[h.ID] = &cp
	return nil
}

func (f *fakeRepo) GetByID(_ context.Context, userID, hiveID uuid.UUID) (*hive.Hive, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.byID[hiveID]
	if !ok || h.UserID != userID || h.DeletedAt != nil {
		return nil, hive.ErrNotFound
	}
	cp := *h
	return &cp, nil
}

func (f *fakeRepo) ListByUser(_ context.Context, userID uuid.UUID, p pagination.Params) ([]*hive.Hive, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []*hive.Hive
	for _, h := range f.byID {
		if h.UserID == userID && h.DeletedAt == nil {
			cp := *h
			all = append(all, &cp)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.Before(all[j].CreatedAt)
		}
		return all[i].ID.String() < all[j].ID.String()
	})

	total := len(all)
	start := p.Offset()
	if start > total {
		start = total
	}
	end := start + p.Limit
	if end > total {
		end = total
	}

	return all[start:end], total, nil
}

func (f *fakeRepo) Update(_ context.Context, h *hive.Hive) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.byID[h.ID]
	if !ok || existing.UserID != h.UserID || existing.DeletedAt != nil {
		return hive.ErrNotFound
	}
	cp := *h
	f.byID[h.ID] = &cp
	return nil
}

func (f *fakeRepo) ListByApiary(_ context.Context, userID, apiaryID uuid.UUID) ([]*hive.Hive, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []*hive.Hive
	for _, h := range f.byID {
		if h.UserID == userID && h.ApiaryID == apiaryID {
			cp := *h
			all = append(all, &cp)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID.String() < all[j].ID.String() })
	return all, nil
}

func (f *fakeRepo) HardDelete(_ context.Context, userID, hiveID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.byID[hiveID]
	if !ok || h.UserID != userID {
		return hive.ErrNotFound
	}
	delete(f.byID, hiveID)
	return nil
}

// --- fake apiary verifier ---

// fakeApiaryVerifier simulates apiary-service: a set of (token, apiaryID)
// pairs are "owned", everything else is rejected exactly like a 404 from
// the real service would be.
type fakeApiaryVerifier struct {
	owned map[string]uuid.UUID // token -> the one apiary it owns
}

func newFakeApiaryVerifier() *fakeApiaryVerifier {
	return &fakeApiaryVerifier{owned: map[string]uuid.UUID{}}
}

func (f *fakeApiaryVerifier) allow(token string, apiaryID uuid.UUID) {
	f.owned[token] = apiaryID
}

func (f *fakeApiaryVerifier) Verify(_ context.Context, accessToken string, apiaryID uuid.UUID) error {
	if owned, ok := f.owned[accessToken]; ok && owned == apiaryID {
		return nil
	}
	return apphive.ErrApiaryNotFound
}

// --- fake inspection/media deleters ---

// fakeInspectionDeleter simulates inspection-service's DeleteByHive: it
// records every hiveID it was asked to delete, and can be configured to
// fail for specific hiveIDs to exercise the cascade's abort-on-failure
// behavior.
type fakeInspectionDeleter struct {
	mu      sync.Mutex
	deleted []uuid.UUID
	failFor map[uuid.UUID]error
}

func newFakeInspectionDeleter() *fakeInspectionDeleter {
	return &fakeInspectionDeleter{failFor: map[uuid.UUID]error{}}
}

func (f *fakeInspectionDeleter) failOn(hiveID uuid.UUID, err error) {
	f.failFor[hiveID] = err
}

func (f *fakeInspectionDeleter) DeleteByHive(_ context.Context, _ string, hiveID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failFor[hiveID]; ok {
		return err
	}
	f.deleted = append(f.deleted, hiveID)
	return nil
}

func (f *fakeInspectionDeleter) wasDeleted(hiveID uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.deleted {
		if id == hiveID {
			return true
		}
	}
	return false
}

// fakeMediaDeleter is the media-service equivalent of
// fakeInspectionDeleter.
type fakeMediaDeleter struct {
	mu      sync.Mutex
	deleted []uuid.UUID
	failFor map[uuid.UUID]error
}

func newFakeMediaDeleter() *fakeMediaDeleter {
	return &fakeMediaDeleter{failFor: map[uuid.UUID]error{}}
}

func (f *fakeMediaDeleter) failOn(hiveID uuid.UUID, err error) {
	f.failFor[hiveID] = err
}

func (f *fakeMediaDeleter) DeleteByOwner(_ context.Context, _ string, hiveID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failFor[hiveID]; ok {
		return err
	}
	f.deleted = append(f.deleted, hiveID)
	return nil
}

func (f *fakeMediaDeleter) wasDeleted(hiveID uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.deleted {
		if id == hiveID {
			return true
		}
	}
	return false
}

// newService builds a Service backed by repo and apiaries, with
// always-succeeding fake inspection/media deleters - the right default
// for every test that isn't specifically exercising the delete cascade.
func newService(repo *fakeRepo, apiaries *fakeApiaryVerifier) *apphive.Service {
	return apphive.NewService(repo, apiaries, newFakeInspectionDeleter(), newFakeMediaDeleter())
}

// --- tests ---

func TestCreate_Success(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "user-token"
	verifier.allow(token, apiaryID)

	h, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
		Location: "North corner",
		Notes:    "strong colony",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if h.UserID != userID {
		t.Errorf("UserID = %s, want %s", h.UserID, userID)
	}
	if h.ApiaryID != apiaryID {
		t.Errorf("ApiaryID = %s, want %s", h.ApiaryID, apiaryID)
	}
}

// TestCreate_ApiaryNotOwnedByCaller is the core cross-service security
// guarantee: a hive can't be created under an apiary the caller doesn't
// own, even if they know its ID.
func TestCreate_ApiaryNotOwnedByCaller(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	someoneElsesApiary := uuid.New()
	// Deliberately not calling verifier.allow for this token/apiary pair.

	_, err := svc.Create(context.Background(), uuid.New(), "attacker-token", apphive.CreateInput{
		ApiaryID: someoneElsesApiary,
		Name:     "Squatter hive",
	})
	if !errors.Is(err, apphive.ErrApiaryNotFound) {
		t.Fatalf("Create under unowned apiary: got %v, want ErrApiaryNotFound", err)
	}
}

func TestCreate_UnknownApiary(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)

	_, err := svc.Create(context.Background(), uuid.New(), "some-token", apphive.CreateInput{
		ApiaryID: uuid.New(),
		Name:     "Hive",
	})
	if !errors.Is(err, apphive.ErrApiaryNotFound) {
		t.Fatalf("Create under unknown apiary: got %v, want ErrApiaryNotFound", err)
	}
}

func TestGet_Success(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "H1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Get(context.Background(), userID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get returned %s, want %s", got.ID, created.ID)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newService(newFakeRepo(), newFakeApiaryVerifier())

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Get with unknown id: got %v, want ErrNotFound", err)
	}
}

// TestGet_WrongOwner_ReturnsNotFound proves ownership is enforced on
// every subsequent read too, not just at creation time.
func TestGet_WrongOwner_ReturnsNotFound(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	owner := uuid.New()
	other := uuid.New()
	apiaryID := uuid.New()
	token := "owner-token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), owner, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Owner's hive"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Get(context.Background(), other, created.ID)
	if !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Get by non-owner: got %v, want ErrNotFound", err)
	}
}

func TestList_ReturnsOnlyOwnHives(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userA := uuid.New()
	userB := uuid.New()
	apiaryA := uuid.New()
	apiaryB := uuid.New()
	tokenA := "token-a"
	tokenB := "token-b"
	verifier.allow(tokenA, apiaryA)
	verifier.allow(tokenB, apiaryB)

	for _, name := range []string{"A1", "A2"} {
		if _, err := svc.Create(context.Background(), userA, tokenA, apphive.CreateInput{ApiaryID: apiaryA, Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if _, err := svc.Create(context.Background(), userB, tokenB, apphive.CreateInput{ApiaryID: apiaryB, Name: "B1"}); err != nil {
		t.Fatalf("create B1: %v", err)
	}

	list, total, err := svc.List(context.Background(), userA, pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("List total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d hives, want 2", len(list))
	}
	for _, h := range list {
		if h.UserID != userA {
			t.Errorf("List leaked hive %s owned by %s into userA's list", h.ID, h.UserID)
		}
	}
}

func TestList_Pagination(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	for i := 0; i < 5; i++ {
		if _, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "H"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	firstPage, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 1, Limit: 2})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(firstPage) != 2 {
		t.Fatalf("page 1 returned %d hives, want 2", len(firstPage))
	}

	lastPage, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 3, Limit: 2})
	if err != nil {
		t.Fatalf("List page 3: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(lastPage) != 1 {
		t.Fatalf("page 3 returned %d hives, want 1", len(lastPage))
	}

	beyond, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 10, Limit: 2})
	if err != nil {
		t.Fatalf("List page 10: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(beyond) != 0 {
		t.Fatalf("page beyond available data returned %d hives, want 0", len(beyond))
	}
}

func TestList_Empty(t *testing.T) {
	svc := newService(newFakeRepo(), newFakeApiaryVerifier())

	list, total, err := svc.List(context.Background(), uuid.New(), pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	if len(list) != 0 {
		t.Fatalf("List = %v, want empty", list)
	}
}

func TestUpdate_Success(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Old name"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(context.Background(), userID, created.ID, apphive.UpdateInput{
		Name:     "New name",
		Location: "New location",
		Notes:    "New notes",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "New name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New name")
	}
	if updated.ApiaryID != apiaryID {
		t.Errorf("ApiaryID changed to %s, want unchanged %s", updated.ApiaryID, apiaryID)
	}
}

func TestUpdate_WrongOwner_ReturnsNotFound(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	owner := uuid.New()
	other := uuid.New()
	apiaryID := uuid.New()
	token := "owner-token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), owner, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Owner's hive"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Update(context.Background(), other, created.ID, apphive.UpdateInput{Name: "Hijacked"})
	if !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Update by non-owner: got %v, want ErrNotFound", err)
	}

	got, err := svc.Get(context.Background(), owner, created.ID)
	if err != nil {
		t.Fatalf("Get after failed hijack attempt: %v", err)
	}
	if got.Name != "Owner's hive" {
		t.Errorf("Name = %q after failed hijack attempt, want unchanged", got.Name)
	}
}

func TestDelete_Success(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Gone soon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, token, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Get(context.Background(), userID, created.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestDelete_WrongOwner_ReturnsNotFoundAndDoesNotDelete(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	owner := uuid.New()
	other := uuid.New()
	apiaryID := uuid.New()
	token := "owner-token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), owner, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Owner's hive"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), other, "attacker-token", created.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Delete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := svc.Get(context.Background(), owner, created.ID); err != nil {
		t.Fatalf("owner's hive should survive a failed delete attempt by another user: %v", err)
	}
}

func TestDelete_CascadesInspectionsAndMediaBeforeHive(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	inspections := newFakeInspectionDeleter()
	media := newFakeMediaDeleter()
	svc := apphive.NewService(repo, verifier, inspections, media)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Gone soon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, token, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !inspections.wasDeleted(created.ID) {
		t.Error("Delete did not cascade to inspection-service")
	}
	if !media.wasDeleted(created.ID) {
		t.Error("Delete did not cascade to media-service")
	}
	if _, err := svc.Get(context.Background(), userID, created.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestDelete_AbortsOnInspectionDeleteFailure_HiveSurvives is the core
// abort-on-failure guarantee: if inspection-service can't be reached (or
// fails for any other reason), the hive itself must not be deleted -
// otherwise its inspections would be permanently orphaned.
func TestDelete_AbortsOnInspectionDeleteFailure_HiveSurvives(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	inspections := newFakeInspectionDeleter()
	media := newFakeMediaDeleter()
	svc := apphive.NewService(repo, verifier, inspections, media)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Survives"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("inspection-service unreachable")
	inspections.failOn(created.ID, boom)

	if err := svc.Delete(context.Background(), userID, token, created.ID); !errors.Is(err, boom) {
		t.Fatalf("Delete: got %v, want %v", err, boom)
	}

	if media.wasDeleted(created.ID) {
		t.Error("media-service was called even though inspection-service failed first")
	}
	if _, err := svc.Get(context.Background(), userID, created.ID); err != nil {
		t.Fatalf("hive should survive when inspection-service fails: %v", err)
	}
}

// TestDelete_AbortsOnMediaDeleteFailure_HiveSurvives mirrors the previous
// test for the second cascade step: media-service failing must also stop
// the hive itself from being deleted, even though inspection-service's
// step already succeeded.
func TestDelete_AbortsOnMediaDeleteFailure_HiveSurvives(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	inspections := newFakeInspectionDeleter()
	media := newFakeMediaDeleter()
	svc := apphive.NewService(repo, verifier, inspections, media)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Survives"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("media-service unreachable")
	media.failOn(created.ID, boom)

	if err := svc.Delete(context.Background(), userID, token, created.ID); !errors.Is(err, boom) {
		t.Fatalf("Delete: got %v, want %v", err, boom)
	}

	if !inspections.wasDeleted(created.ID) {
		t.Error("inspection-service should have already been called before media-service failed")
	}
	if _, err := svc.Get(context.Background(), userID, created.ID); err != nil {
		t.Fatalf("hive should survive when media-service fails: %v", err)
	}
}

func TestDeleteByApiary_CascadesEveryHive(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	inspections := newFakeInspectionDeleter()
	media := newFakeMediaDeleter()
	svc := apphive.NewService(repo, verifier, inspections, media)
	userID := uuid.New()
	apiaryID := uuid.New()
	otherApiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	var ids []uuid.UUID
	for _, name := range []string{"H1", "H2"} {
		created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: name})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		ids = append(ids, created.ID)
	}
	// A hive under a different apiary must survive.
	verifier.allow("other-token", otherApiaryID)
	keep, err := svc.Create(context.Background(), userID, "other-token", apphive.CreateInput{ApiaryID: otherApiaryID, Name: "Keep"})
	if err != nil {
		t.Fatalf("create keep: %v", err)
	}

	if err := svc.DeleteByApiary(context.Background(), userID, token, apiaryID); err != nil {
		t.Fatalf("DeleteByApiary: %v", err)
	}

	for _, id := range ids {
		if _, err := svc.Get(context.Background(), userID, id); !errors.Is(err, hive.ErrNotFound) {
			t.Errorf("hive %s survived DeleteByApiary: got %v, want ErrNotFound", id, err)
		}
		if !inspections.wasDeleted(id) || !media.wasDeleted(id) {
			t.Errorf("hive %s: cascade did not reach inspection-service/media-service", id)
		}
	}
	if _, err := svc.Get(context.Background(), userID, keep.ID); err != nil {
		t.Fatalf("hive under a different apiary should survive: %v", err)
	}
}

// TestDeleteByApiary_AbortsOnFirstFailure_EarlierHivesStayDeleted is the
// direct proof of the no-rollback contract: when a hive partway through
// the loop fails to cascade, hives already fully deleted earlier in the
// same call stay deleted - they are not resurrected, and the loop simply
// stops rather than continuing past the failure.
func TestDeleteByApiary_AbortsOnFirstFailure_EarlierHivesStayDeleted(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	inspections := newFakeInspectionDeleter()
	media := newFakeMediaDeleter()
	svc := apphive.NewService(repo, verifier, inspections, media)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	if _, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "A"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "B"}); err != nil {
		t.Fatalf("create b: %v", err)
	}

	// fakeRepo.ListByApiary sorts by ID, so determine that order directly
	// rather than relying on random UUID ordering, and fail the
	// second-visited hive so the assertions below are deterministic.
	visited, err := repo.ListByApiary(context.Background(), userID, apiaryID)
	if err != nil {
		t.Fatalf("ListByApiary: %v", err)
	}
	if len(visited) != 2 {
		t.Fatalf("ListByApiary returned %d hives, want 2", len(visited))
	}
	firstVisited, secondVisited := visited[0].ID, visited[1].ID
	boom := errors.New("inspection-service unreachable")
	inspections.failOn(secondVisited, boom)

	err = svc.DeleteByApiary(context.Background(), userID, token, apiaryID)
	if !errors.Is(err, boom) {
		t.Fatalf("DeleteByApiary: got %v, want %v", err, boom)
	}

	if !errorsIsNotFound(svc, userID, firstVisited) {
		t.Error("the hive visited before the failure should have been fully cascaded and deleted")
	}
	if errorsIsNotFound(svc, userID, secondVisited) {
		t.Error("the hive whose cascade failed must not be deleted")
	}
}

func errorsIsNotFound(svc *apphive.Service, userID, hiveID uuid.UUID) bool {
	_, err := svc.Get(context.Background(), userID, hiveID)
	return errors.Is(err, hive.ErrNotFound)
}
