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

func (f *fakeRepo) Delete(_ context.Context, userID, hiveID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.byID[hiveID]
	if !ok || h.UserID != userID || h.DeletedAt != nil {
		return hive.ErrNotFound
	}
	now := h.UpdatedAt
	h.DeletedAt = &now
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

// --- tests ---

func TestCreate_Success(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), verifier)

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
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), newFakeApiaryVerifier())

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Get with unknown id: got %v, want ErrNotFound", err)
	}
}

// TestGet_WrongOwner_ReturnsNotFound proves ownership is enforced on
// every subsequent read too, not just at creation time.
func TestGet_WrongOwner_ReturnsNotFound(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), newFakeApiaryVerifier())

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
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), verifier)
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
	svc := apphive.NewService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Gone soon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Get(context.Background(), userID, created.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestDelete_WrongOwner_ReturnsNotFoundAndDoesNotDelete(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := apphive.NewService(newFakeRepo(), verifier)
	owner := uuid.New()
	other := uuid.New()
	apiaryID := uuid.New()
	token := "owner-token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), owner, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Owner's hive"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), other, created.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Delete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := svc.Get(context.Background(), owner, created.ID); err != nil {
		t.Fatalf("owner's hive should survive a failed delete attempt by another user: %v", err)
	}
}
