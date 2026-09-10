package hive_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	if f.hasActiveNameLocked(h.ApiaryID, h.Name, h.ID) {
		return hive.ErrNameTaken
	}
	cp := *h
	f.byID[h.ID] = &cp
	return nil
}

func (f *fakeRepo) CountByUser(_ context.Context, userID uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, h := range f.byID {
		if h.UserID == userID && h.DeletedAt == nil {
			count++
		}
	}
	return count, nil
}

func (f *fakeRepo) CreateWithLimit(ctx context.Context, h *hive.Hive, maxCount int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasActiveNameLocked(h.ApiaryID, h.Name, h.ID) {
		return hive.ErrNameTaken
	}
	if maxCount > 0 {
		count := 0
		for _, existing := range f.byID {
			if existing.UserID == h.UserID && existing.DeletedAt == nil {
				count++
			}
		}
		if count >= maxCount {
			return hive.ErrLimitReached
		}
	}
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

func (f *fakeRepo) ListByUser(_ context.Context, userID uuid.UUID, p pagination.Params, search *string) ([]*hive.Hive, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []*hive.Hive
	for _, h := range f.byID {
		if h.UserID == userID && h.DeletedAt == nil {
			if search != nil && len(*search) >= 3 {
				s := strings.ToLower(*search)
				if !strings.Contains(strings.ToLower(h.Name), s) && !strings.Contains(strings.ToLower(h.Notes), s) {
					continue
				}
			}
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
	if f.hasActiveNameLocked(h.ApiaryID, h.Name, h.ID) {
		return hive.ErrNameTaken
	}
	cp := *h
	f.byID[h.ID] = &cp
	return nil
}

func (f *fakeRepo) hasActiveNameLocked(apiaryID uuid.UUID, name string, excludeID uuid.UUID) bool {
	for id, existing := range f.byID {
		if id != excludeID && existing.ApiaryID == apiaryID && existing.DeletedAt == nil && existing.Name == name {
			return true
		}
	}
	return false
}

func (f *fakeRepo) ListByApiary(_ context.Context, userID, apiaryID uuid.UUID, p pagination.Params, search *string) ([]*hive.Hive, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []*hive.Hive
	for _, h := range f.byID {
		if h.UserID == userID && h.ApiaryID == apiaryID && h.DeletedAt == nil {
			if search != nil && len(*search) >= 3 {
				s := strings.ToLower(*search)
				if !strings.Contains(strings.ToLower(h.Name), s) && !strings.Contains(strings.ToLower(h.Notes), s) {
					continue
				}
			}
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

func (f *fakeRepo) ListAllByApiary(_ context.Context, userID, apiaryID uuid.UUID) ([]*hive.Hive, error) {
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
	owned map[string]map[uuid.UUID]bool
}

func newFakeApiaryVerifier() *fakeApiaryVerifier {
	return &fakeApiaryVerifier{owned: map[string]map[uuid.UUID]bool{}}
}

func (f *fakeApiaryVerifier) allow(token string, apiaryID uuid.UUID) {
	if f.owned[token] == nil {
		f.owned[token] = map[uuid.UUID]bool{}
	}
	f.owned[token][apiaryID] = true
}

func (f *fakeApiaryVerifier) Verify(_ context.Context, accessToken string, apiaryID uuid.UUID) error {
	if apiaries, ok := f.owned[accessToken]; ok && apiaries[apiaryID] {
		return nil
	}
	return apphive.ErrApiaryNotFound
}

// --- fake inspection deleter ---

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

// fakeMediaClient stands in for application/hive.MediaClient: ownedIDs is
// the set of media ids VerifyOwnership will accept as belonging to the
// caller (seeded via own()), and deletedIDs records every id ever passed
// to DeleteByIDs, in calls not suppressed by failDeleteWith.
type fakeMediaClient struct {
	mu         sync.Mutex
	ownedIDs   map[uuid.UUID]bool
	deletedIDs []uuid.UUID
	deleteErr  error
}

func newFakeMediaClient() *fakeMediaClient {
	return &fakeMediaClient{ownedIDs: map[uuid.UUID]bool{}}
}

// own registers each of ids as belonging to the caller, so VerifyOwnership
// accepts it.
func (f *fakeMediaClient) own(ids ...uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		f.ownedIDs[id] = true
	}
}

// failDeleteWith makes every subsequent DeleteByIDs call fail with err,
// simulating media-service being unreachable.
func (f *fakeMediaClient) failDeleteWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteErr = err
}

func (f *fakeMediaClient) VerifyOwnership(_ context.Context, _ string, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		if !f.ownedIDs[id] {
			return apphive.ErrImageNotFound
		}
	}
	return nil
}

func (f *fakeMediaClient) DeleteByIDs(_ context.Context, _ string, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedIDs = append(f.deletedIDs, ids...)
	return nil
}

func (f *fakeMediaClient) wasDeleted(id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.deletedIDs {
		if d == id {
			return true
		}
	}
	return false
}

func (f *fakeMediaClient) deleteCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deletedIDs)
}

type fakeSubscriptionClient struct {
	entitlement string
	err         error
}

func newFakeSubscriptionClient() *fakeSubscriptionClient {
	return &fakeSubscriptionClient{entitlement: apphive.EntitlementPro}
}

func (f *fakeSubscriptionClient) GetEntitlement(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.entitlement, nil
}

// newService builds a Service backed by repo and apiaries, with
// always-succeeding fake inspection/media/subscription clients - the right default
// for every test that isn't specifically exercising the delete cascade,
// images, or entitlement limits.
func newService(repo *fakeRepo, apiaries *fakeApiaryVerifier) *apphive.Service {
	return apphive.NewService(repo, apiaries, newFakeInspectionDeleter(), newFakeMediaClient(), newFakeSubscriptionClient())
}

func mustCreate(t *testing.T, repo *fakeRepo, h *hive.Hive) {
	t.Helper()
	if err := repo.Create(context.Background(), h); err != nil {
		t.Fatalf("seed hive: %v", err)
	}
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
	if len(h.Images) != 0 {
		t.Errorf("Images = %v, want empty", h.Images)
	}
}

func TestCreate_DuplicateNameWithinSameApiary_ReturnsNameTaken(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	svc := newService(repo, verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	verifier.allow("token", apiaryID)

	_, err := svc.Create(context.Background(), userID, "token", apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
	})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err = svc.Create(context.Background(), userID, "token", apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
	})
	if !errors.Is(err, hive.ErrNameTaken) {
		t.Fatalf("second Create: got %v, want ErrNameTaken", err)
	}
}

func TestCreate_SameNameInDifferentApiariesSucceeds(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiary1 := uuid.New()
	apiary2 := uuid.New()
	verifier.allow("token", apiary1)
	verifier.allow("token", apiary2)

	if _, err := svc.Create(context.Background(), userID, "token", apphive.CreateInput{ApiaryID: apiary1, Name: "Hive 1"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := svc.Create(context.Background(), userID, "token", apphive.CreateInput{ApiaryID: apiary2, Name: "Hive 1"}); err != nil {
		t.Fatalf("same name in another apiary should succeed: %v", err)
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

// TestCreate_WithImages_Success proves a hive can be created with photos
// already referenced, without a separate PUT: ownership of every id is
// verified against media-service before the hive is persisted.
func TestCreate_WithImages_Success(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	photo1 := uuid.New()
	photo2 := uuid.New()
	media.own(photo1, photo2)

	h, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
		Images:   []uuid.UUID{photo1, photo2, photo1}, // duplicated on purpose
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(h.Images) != 2 || h.Images[0] != photo1 || h.Images[1] != photo2 {
		t.Fatalf("Images = %v, want [%s, %s] deduplicated", h.Images, photo1, photo2)
	}

	got, err := repo.GetByID(context.Background(), userID, h.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Images) != 2 {
		t.Fatalf("persisted Images = %v, want 2 entries", got.Images)
	}
}

// TestCreate_WithImages_RejectsForeignMedia proves Create validates
// ownership of every referenced image before persisting anything - since
// verification happens first, a rejected image means no hive is ever
// created (no rollback needed, unlike the old attach-after-insert design).
func TestCreate_WithImages_RejectsForeignMedia(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	media := newFakeMediaClient() // foreign is deliberately never own()'d
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	foreign := uuid.New()

	_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
		Images:   []uuid.UUID{foreign},
	})
	if !errors.Is(err, apphive.ErrImageNotFound) {
		t.Fatalf("Create with foreign media: got %v, want ErrImageNotFound", err)
	}

	list, _, err := repo.ListByUser(context.Background(), userID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("hive was persisted despite a rejected image: %v", list)
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

	list, total, err := svc.List(context.Background(), userA, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil)
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
		if _, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: fmt.Sprintf("H-%d", i)}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	firstPage, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 1, Limit: 2}, nil)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(firstPage) != 2 {
		t.Fatalf("page 1 returned %d hives, want 2", len(firstPage))
	}

	lastPage, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 3, Limit: 2}, nil)
	if err != nil {
		t.Fatalf("List page 3: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(lastPage) != 1 {
		t.Fatalf("page 3 returned %d hives, want 1", len(lastPage))
	}

	beyond, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 10, Limit: 2}, nil)
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

	list, total, err := svc.List(context.Background(), uuid.New(), pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil)
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

func TestListByApiary_ReturnsOnlyOwnHivesForThatApiary(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userA := uuid.New()
	userB := uuid.New()
	apiaryA1 := uuid.New()
	apiaryA2 := uuid.New()
	apiaryB := uuid.New()
	tokenA := "token-a"
	tokenB := "token-b"
	verifier.allow(tokenA, apiaryA1)
	verifier.allow(tokenA, apiaryA2)
	verifier.allow(tokenB, apiaryB)

	for _, name := range []string{"A1-Hive1", "A1-Hive2"} {
		if _, err := svc.Create(context.Background(), userA, tokenA, apphive.CreateInput{ApiaryID: apiaryA1, Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if _, err := svc.Create(context.Background(), userA, tokenA, apphive.CreateInput{ApiaryID: apiaryA2, Name: "A2-Hive1"}); err != nil {
		t.Fatalf("create A2-Hive1: %v", err)
	}
	if _, err := svc.Create(context.Background(), userB, tokenB, apphive.CreateInput{ApiaryID: apiaryB, Name: "B-Hive1"}); err != nil {
		t.Fatalf("create B-Hive1: %v", err)
	}

	list, total, err := svc.ListByApiary(context.Background(), userA, tokenA, apiaryA1, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil)
	if err != nil {
		t.Fatalf("ListByApiary: %v", err)
	}
	if total != 2 {
		t.Fatalf("ListByApiary total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Fatalf("ListByApiary returned %d hives, want 2", len(list))
	}
	for _, h := range list {
		if h.UserID != userA || h.ApiaryID != apiaryA1 {
			t.Errorf("ListByApiary leaked hive %s (user %s, apiary %s)", h.ID, h.UserID, h.ApiaryID)
		}
	}
}

func TestListByApiary_Pagination(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	for i := 0; i < 5; i++ {
		if _, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: fmt.Sprintf("H-%d", i)}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	firstPage, total, err := svc.ListByApiary(context.Background(), userID, token, apiaryID, pagination.Params{Page: 1, Limit: 2}, nil)
	if err != nil {
		t.Fatalf("ListByApiary page 1: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(firstPage) != 2 {
		t.Fatalf("page 1 returned %d hives, want 2", len(firstPage))
	}

	lastPage, total, err := svc.ListByApiary(context.Background(), userID, token, apiaryID, pagination.Params{Page: 3, Limit: 2}, nil)
	if err != nil {
		t.Fatalf("ListByApiary page 3: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(lastPage) != 1 {
		t.Fatalf("page 3 returned %d hives, want 1", len(lastPage))
	}

	beyond, total, err := svc.ListByApiary(context.Background(), userID, token, apiaryID, pagination.Params{Page: 10, Limit: 2}, nil)
	if err != nil {
		t.Fatalf("ListByApiary page 10: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(beyond) != 0 {
		t.Fatalf("page beyond available data returned %d hives, want 0", len(beyond))
	}
}

func TestListByApiary_Search(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	inputs := []apphive.CreateInput{
		{ApiaryID: apiaryID, Name: "Queen Carnica", Notes: "Strong colony"},
		{ApiaryID: apiaryID, Name: "Italian Hive", Notes: "Golden queen inside"},
		{ApiaryID: apiaryID, Name: "Buckfast", Notes: "Swarm control done"},
	}
	for _, in := range inputs {
		if _, err := svc.Create(context.Background(), userID, token, in); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	search := "queen"
	list, total, err := svc.ListByApiary(context.Background(), userID, token, apiaryID, pagination.Params{Page: 1, Limit: 10}, &search)
	if err != nil {
		t.Fatalf("ListByApiary search: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}

	searchWorker := "swarm"
	list, total, err = svc.ListByApiary(context.Background(), userID, token, apiaryID, pagination.Params{Page: 1, Limit: 10}, &searchWorker)
	if err != nil {
		t.Fatalf("ListByApiary search: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
}

func TestListByApiary_Empty(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	list, total, err := svc.ListByApiary(context.Background(), userID, token, apiaryID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil)
	if err != nil {
		t.Fatalf("ListByApiary: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	if len(list) != 0 {
		t.Fatalf("ListByApiary = %v, want empty", list)
	}
}

func TestListByApiary_UnownedApiaryReturnsErrApiaryNotFound(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	svc := newService(newFakeRepo(), verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	// Do not allow token for apiaryID

	_, _, err := svc.ListByApiary(context.Background(), userID, token, apiaryID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit}, nil)
	if !errors.Is(err, apphive.ErrApiaryNotFound) {
		t.Fatalf("ListByApiary on unowned apiary: got %v, want ErrApiaryNotFound", err)
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

	updated, err := svc.Update(context.Background(), userID, "token", created.ID, apphive.UpdateInput{
		Name:  "New name",
		Notes: "New notes",
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

func TestUpdate_UnchangedNameRemainsValid(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	svc := newService(repo, verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	h := hive.New(userID, apiaryID, "Hive 1", "old notes")
	mustCreate(t, repo, h)

	updated, err := svc.Update(context.Background(), userID, "token", h.ID, apphive.UpdateInput{
		Name:  "Hive 1",
		Notes: "new notes",
	})
	if err != nil {
		t.Fatalf("Update with unchanged name: %v", err)
	}
	if updated.Name != "Hive 1" || updated.Notes != "new notes" {
		t.Fatalf("updated = %+v, want unchanged name with new notes", updated)
	}
}

func TestUpdate_DuplicateNameWithinSameApiary_ReturnsNameTaken(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	svc := newService(repo, verifier)
	userID := uuid.New()
	apiaryID := uuid.New()
	first := hive.New(userID, apiaryID, "First", "")
	second := hive.New(userID, apiaryID, "Second", "")
	mustCreate(t, repo, first)
	mustCreate(t, repo, second)

	_, err := svc.Update(context.Background(), userID, "token", second.ID, apphive.UpdateInput{Name: "First"})
	if !errors.Is(err, hive.ErrNameTaken) {
		t.Fatalf("Update duplicate name: got %v, want ErrNameTaken", err)
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

	_, err = svc.Update(context.Background(), other, "token", created.ID, apphive.UpdateInput{Name: "Hijacked"})
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

// TestUpdate_ImagesNil_LeavesImagesUntouched proves that omitting Images
// from an update (the nil case) is a no-op: a client updating just the
// name must never accidentally clear every photo reference.
func TestUpdate_ImagesNil_LeavesImagesUntouched(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	mediaID := uuid.New()
	media.own(mediaID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
		Images:   []uuid.UUID{mediaID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(context.Background(), userID, token, created.ID, apphive.UpdateInput{Name: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != mediaID {
		t.Fatalf("Images = %v, want [%s] (untouched)", updated.Images, mediaID)
	}
}

// TestUpdate_ImagesEmpty_ClearsReferencesWithoutDeletingFiles proves that
// explicitly sending an empty images list (as opposed to omitting the
// field) clears the hive's reference list - but, critically, does NOT
// delete the underlying media file: removing a reference and deleting a
// file are two separate actions now (the file stays until something
// explicitly calls DELETE /media/{id}, or the whole hive is deleted).
func TestUpdate_ImagesEmpty_ClearsReferencesWithoutDeletingFiles(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	mediaID := uuid.New()
	media.own(mediaID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
		Images:   []uuid.UUID{mediaID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	empty := []uuid.UUID{}
	updated, err := svc.Update(context.Background(), userID, token, created.ID, apphive.UpdateInput{
		Name:   "Renamed",
		Images: &empty,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 0 {
		t.Fatalf("Images = %v, want empty", updated.Images)
	}
	if media.wasDeleted(mediaID) {
		t.Error("removing an image reference must not delete the underlying file")
	}
}

// TestUpdate_ImagesReplacedWholesale proves an update's images list fully
// replaces the previous one (deduplicated) - the dropped id's file
// survives untouched, since remove-from-hive and delete-the-file are
// separate actions now.
func TestUpdate_ImagesReplacedWholesale(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	keep := uuid.New()
	drop := uuid.New()
	media.own(keep, drop)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
		Images:   []uuid.UUID{keep, drop},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	desired := []uuid.UUID{keep, keep} // duplicated on purpose
	updated, err := svc.Update(context.Background(), userID, token, created.ID, apphive.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != keep {
		t.Fatalf("Images = %v, want [%s] deduplicated", updated.Images, keep)
	}
	if media.wasDeleted(drop) {
		t.Error("drop's file must survive - dropping a reference doesn't delete it")
	}
}

// TestUpdate_ImagesRejectsForeignMedia proves that an update can't
// reference a media id that isn't the caller's own, and, critically,
// leaves the hive's images and other fields completely unchanged.
func TestUpdate_ImagesRejectsForeignMedia(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	media := newFakeMediaClient() // foreign is deliberately never own()'d
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	kept := uuid.New()
	media.own(kept)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Hive 1",
		Images:   []uuid.UUID{kept},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	foreign := uuid.New()
	desired := []uuid.UUID{foreign}
	_, err = svc.Update(context.Background(), userID, token, created.ID, apphive.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if !errors.Is(err, apphive.ErrImageNotFound) {
		t.Fatalf("Update with foreign media: got %v, want ErrImageNotFound", err)
	}

	got, err := repo.GetByID(context.Background(), userID, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Hive 1" {
		t.Errorf("Name = %q after rejected update, want unchanged %q", got.Name, "Hive 1")
	}
	if len(got.Images) != 1 || got.Images[0] != kept {
		t.Errorf("Images = %v after rejected update, want unchanged [%s]", got.Images, kept)
	}
}

// TestUpdate_ImagesAcceptsNewlyOwnedMedia proves an id the caller uploaded
// after this hive was created can be added via a plain Update, once
// media-service confirms it belongs to them.
func TestUpdate_ImagesAcceptsNewlyOwnedMedia(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "Hive 1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fresh := uuid.New()
	media.own(fresh)

	desired := []uuid.UUID{fresh}
	updated, err := svc.Update(context.Background(), userID, token, created.ID, apphive.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != fresh {
		t.Fatalf("Images = %v, want [%s]", updated.Images, fresh)
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

// TestDelete_CascadesInspectionsAndImagesBeforeHive proves the full
// cascade: inspection-service is asked to delete first, then every media
// file this hive itself references is hard-deleted, then the hive itself.
func TestDelete_CascadesInspectionsAndImagesBeforeHive(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	inspections := newFakeInspectionDeleter()
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, inspections, media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	photo := uuid.New()
	media.own(photo)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Gone soon",
		Images:   []uuid.UUID{photo},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, token, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !inspections.wasDeleted(created.ID) {
		t.Error("Delete did not cascade to inspection-service")
	}
	if !media.wasDeleted(photo) {
		t.Error("Delete did not cascade to media-service for the hive's own image")
	}
	if _, err := svc.Get(context.Background(), userID, created.ID); !errors.Is(err, hive.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestDelete_SkipsMediaCallWhenNoImages proves Delete never bothers
// calling media-service for a hive with no images - even one configured
// to fail, proving the call is genuinely skipped, not just coincidentally
// successful.
func TestDelete_SkipsMediaCallWhenNoImages(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	inspections := newFakeInspectionDeleter()
	media := newFakeMediaClient()
	media.failDeleteWith(errors.New("should never be called"))
	svc := apphive.NewService(repo, verifier, inspections, media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "No photos"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, token, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if media.deleteCallCount() != 0 {
		t.Error("DeleteByIDs was called even though the hive had no images")
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
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, inspections, media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	photo := uuid.New()
	media.own(photo)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Survives",
		Images:   []uuid.UUID{photo},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("inspection-service unreachable")
	inspections.failOn(created.ID, boom)

	if err := svc.Delete(context.Background(), userID, token, created.ID); !errors.Is(err, boom) {
		t.Fatalf("Delete: got %v, want %v", err, boom)
	}

	if media.deleteCallCount() != 0 {
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
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, inspections, media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	photo := uuid.New()
	media.own(photo)

	created, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Survives",
		Images:   []uuid.UUID{photo},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("media-service unreachable")
	media.failDeleteWith(boom)

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
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, inspections, media, newFakeSubscriptionClient())
	userID := uuid.New()
	apiaryID := uuid.New()
	otherApiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)
	photo1 := uuid.New()
	photo2 := uuid.New()
	media.own(photo1, photo2)

	created1, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "H1", Images: []uuid.UUID{photo1}})
	if err != nil {
		t.Fatalf("create H1: %v", err)
	}
	created2, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{ApiaryID: apiaryID, Name: "H2", Images: []uuid.UUID{photo2}})
	if err != nil {
		t.Fatalf("create H2: %v", err)
	}
	ids := []uuid.UUID{created1.ID, created2.ID}
	photos := []uuid.UUID{photo1, photo2}

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
		if !inspections.wasDeleted(id) {
			t.Errorf("hive %s: cascade did not reach inspection-service", id)
		}
	}
	for _, photo := range photos {
		if !media.wasDeleted(photo) {
			t.Errorf("image %s: cascade did not reach media-service", photo)
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
	media := newFakeMediaClient()
	svc := apphive.NewService(repo, verifier, inspections, media, newFakeSubscriptionClient())
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

	// fakeRepo.ListAllByApiary sorts by ID, so determine that order directly
	// rather than relying on random UUID ordering, and fail the
	// second-visited hive so the assertions below are deterministic.
	visited, err := repo.ListAllByApiary(context.Background(), userID, apiaryID)
	if err != nil {
		t.Fatalf("ListAllByApiary: %v", err)
	}
	if len(visited) != 2 {
		t.Fatalf("ListAllByApiary returned %d hives, want 2", len(visited))
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

func TestCreate_FreeTier_FiveHivesSucceed(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementFree}
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), newFakeMediaClient(), subs)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	for i := 1; i <= 5; i++ {
		h, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
			ApiaryID: apiaryID,
			Name:     fmt.Sprintf("Hive %d", i),
		})
		if err != nil {
			t.Fatalf("creation of hive %d failed for free tier: %v", i, err)
		}
		if h == nil {
			t.Fatalf("expected non-nil hive %d", i)
		}
	}

	count, err := repo.CountByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 hives, got %d", count)
	}
}

func TestCreate_FreeTier_SixthHiveRejected(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementFree}
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), newFakeMediaClient(), subs)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	for i := 1; i <= 5; i++ {
		_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
			ApiaryID: apiaryID,
			Name:     fmt.Sprintf("Hive %d", i),
		})
		if err != nil {
			t.Fatalf("creation of hive %d failed: %v", i, err)
		}
	}

	_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Sixth Hive",
	})
	if !errors.Is(err, apphive.ErrHiveLimitReached) {
		t.Fatalf("expected ErrHiveLimitReached for sixth hive on free tier, got: %v", err)
	}
}

func TestCreate_FreeTier_HivesCountedAcrossMultipleApiaries(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementFree}
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), newFakeMediaClient(), subs)
	userID := uuid.New()
	apiary1 := uuid.New()
	apiary2 := uuid.New()
	token := "token"
	verifier.allow(token, apiary1)
	verifier.allow(token, apiary2)

	// Create 3 hives in apiary1
	for i := 1; i <= 3; i++ {
		_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
			ApiaryID: apiary1,
			Name:     fmt.Sprintf("Apiary1-Hive %d", i),
		})
		if err != nil {
			t.Fatalf("create failed in apiary1: %v", err)
		}
	}

	// Create 2 hives in apiary2 (total = 5)
	for i := 1; i <= 2; i++ {
		_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
			ApiaryID: apiary2,
			Name:     fmt.Sprintf("Apiary2-Hive %d", i),
		})
		if err != nil {
			t.Fatalf("create failed in apiary2: %v", err)
		}
	}

	// 6th hive in apiary2 must be rejected because user already has 5 hives total
	_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiary2,
		Name:     "Apiary2-Hive 3",
	})
	if !errors.Is(err, apphive.ErrHiveLimitReached) {
		t.Fatalf("expected ErrHiveLimitReached across apiaries on free tier, got: %v", err)
	}
}

func TestCreate_ProTier_UnlimitedHivesSucceed(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	subs := &fakeSubscriptionClient{entitlement: apphive.EntitlementPro}
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), newFakeMediaClient(), subs)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	for i := 1; i <= 10; i++ {
		_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
			ApiaryID: apiaryID,
			Name:     fmt.Sprintf("Pro Hive %d", i),
		})
		if err != nil {
			t.Fatalf("creation of pro hive %d failed: %v", i, err)
		}
	}

	count, err := repo.CountByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 10 {
		t.Fatalf("expected 10 hives for pro user, got %d", count)
	}
}

func TestCreate_SubscriptionLookupFailure_FailsClosed(t *testing.T) {
	verifier := newFakeApiaryVerifier()
	repo := newFakeRepo()
	subs := &fakeSubscriptionClient{err: errors.New("subscription-service down")}
	svc := apphive.NewService(repo, verifier, newFakeInspectionDeleter(), newFakeMediaClient(), subs)
	userID := uuid.New()
	apiaryID := uuid.New()
	token := "token"
	verifier.allow(token, apiaryID)

	_, err := svc.Create(context.Background(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     "Attempted Hive",
	})
	if err == nil {
		t.Fatal("expected creation to fail when subscription-service fails")
	}
	if errors.Is(err, apphive.ErrHiveLimitReached) {
		t.Fatal("should not falsely report ErrHiveLimitReached when subscription-service fails")
	}

	count, _ := repo.CountByUser(context.Background(), userID)
	if count != 0 {
		t.Fatalf("no hive should have been created on lookup failure, got count %d", count)
	}
}
