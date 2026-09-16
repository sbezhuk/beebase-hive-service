// Package hive implements the hive use cases: create, get, list, update,
// and delete. It depends only on the domain/hive port and the
// ApiaryVerifier port declared in this package, never on HTTP or
// PostgreSQL directly.
package hive

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/inspectionwarning"
	"github.com/sbezhuk/beebase-common/pagination"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// Service implements the hive use cases. Every method takes the
// requesting user's ID (extracted from their verified access token by the
// transport layer) and passes it straight through to the repository,
// which enforces ownership at the query level.
type Service struct {
	hives            hive.Repository
	apiaries         ApiaryVerifier
	inspections      InspectionDeleter
	inspectionStatus InspectionStatusProvider
	media            MediaClient
	subscriptions    EntitlementResolver
	harvests         HarvestDeleter
	reminders        EntityCleanup
}

// NewService constructs a Service.
func NewService(hives hive.Repository, apiaries ApiaryVerifier, inspections InspectionDeleter, inspectionStatus InspectionStatusProvider, media MediaClient, subscriptions EntitlementResolver, extras ...any) *Service {
	s := &Service{hives: hives, apiaries: apiaries, inspections: inspections, inspectionStatus: inspectionStatus, media: media, subscriptions: subscriptions}
	for _, extra := range extras {
		switch v := extra.(type) {
		case HarvestDeleter:
			s.harvests = v
		case EntityCleanup:
			s.reminders = v
		}
	}
	return s
}

// Create creates a new hive owned by userID under in.ApiaryID, after
// confirming with apiary-service that userID actually owns that apiary.
// accessToken is the caller's own access token, forwarded to
// apiary-service so it can run its own, identical ownership check rather
// than this service trusting a client-supplied user/apiary pairing. If
// in.Images is non-empty, it's deduplicated (preserving first-seen order)
// and every id's ownership is verified against media-service (see
// MediaClient.VerifyOwnership) before anything is persisted; if
// verification fails, Create returns the error immediately, having
// created nothing - there is no rollback to do, unlike the old
// attach-after-insert flow this replaced.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, accessToken string, in CreateInput) (*WithAccess, error) {
	apiaryWritable, err := s.apiaries.Verify(ctx, accessToken, in.ApiaryID)
	if err != nil {
		return nil, err
	}

	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("hive: resolve entitlement: %w", err)
	}

	maxHives := 0 // 0 means unlimited
	if entitlement == EntitlementFree {
		// A Free user may only ever create into their one writable
		// apiary - creating under a currently read-only apiary is
		// rejected outright, regardless of how many hives they have
		// elsewhere.
		if !apiaryWritable {
			return nil, ErrParentReadOnly
		}
		// The 5-hive Free limit is account-wide, not scoped to this
		// apiary: FreeMaxHives is a per-user quota (see FreeMaxHives),
		// independent of how many apiaries the count is spread across.
		// Parent-apiary writability (checked above) is what actually
		// gates whether a hive can be created at all; this count only
		// ever guards how many the account may hold in total.
		count, err := s.hives.CountByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("hive: count hives: %w", err)
		}
		if count >= FreeMaxHives {
			return nil, ErrHiveLimitReached
		}
		maxHives = FreeMaxHives
	}

	dedup := dedupeImages(in.Images)
	if len(dedup) > MaxMediaAttachments {
		return nil, ErrMediaLimitReached
	}
	if len(dedup) > 0 {
		if err := s.media.VerifyOwnership(ctx, accessToken, dedup); err != nil {
			return nil, err
		}
	}

	h := hive.New(userID, in.ApiaryID, in.Name, in.Notes)
	h.Images = dedup

	if err := s.hives.CreateWithLimit(ctx, h, maxHives); err != nil {
		if errors.Is(err, hive.ErrLimitReached) {
			return nil, ErrHiveLimitReached
		}
		return nil, fmt.Errorf("hive: create: %w", err)
	}

	// A just-created hive is always writable: under Pro nothing is ever
	// restricted; under Free, it was only allowed to be created because
	// its parent apiary was just proven writable and the count check above
	// proved it ranks within the first FreeMaxHives hives in that apiary.
	return &WithAccess{Hive: h, Writable: true}, nil
}

// Get returns the hive identified by hiveID, if it belongs to userID -
// including the media ids it references (Hive.Images), read straight from
// the row rather than a media-service round trip - along with whether
// it's currently writable for the caller (see isWritable). accessToken is
// the caller's own access token, forwarded to subscription-service and
// apiary-service to resolve entitlement and parent-apiary writability.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, accessToken string, hiveID uuid.UUID) (*WithAccess, error) {
	h, err := s.hives.GetByID(ctx, userID, hiveID)
	if err != nil {
		return nil, err
	}

	writable, err := s.resolveWritable(ctx, userID, accessToken, h)
	if err != nil {
		return nil, err
	}

	return &WithAccess{Hive: h, Writable: writable}, nil
}

// resolveWritable reports whether h is currently writable for the caller:
// always true under Pro, otherwise delegates to isWritable. accessToken is
// forwarded to subscription-service.
func (s *Service) resolveWritable(ctx context.Context, userID uuid.UUID, accessToken string, h *hive.Hive) (bool, error) {
	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return false, fmt.Errorf("hive: resolve entitlement: %w", err)
	}
	if entitlement != EntitlementFree {
		return true, nil
	}
	return s.isWritable(ctx, userID, accessToken, h)
}

// isWritable reports whether h is currently writable under the caller's
// Free entitlement: its parent apiary must itself be writable (asked of
// apiary-service, the sole source of truth for apiary ordering - see
// ApiaryVerifier.Verify), and h must rank among userID's first
// FreeMaxHives hives account-wide by (created_at, id) - not merely within
// its own apiary. FreeMaxHives is a per-user quota (see FreeMaxHives), so
// a hive's rank is always evaluated against every hive the user owns,
// regardless of apiary; the parent-apiary check is what actually excludes
// hives sitting in a locked apiary; it's not folded into the ranking
// pool itself. A hive under a read-only apiary can never be writable no
// matter its rank. Recomputed from current data on every call, the same
// way apiary-service's isWritable is.
func (s *Service) isWritable(ctx context.Context, userID uuid.UUID, accessToken string, h *hive.Hive) (bool, error) {
	apiaryWritable, err := s.apiaries.Verify(ctx, accessToken, h.ApiaryID)
	if err != nil {
		return false, err
	}
	if !apiaryWritable {
		return false, nil
	}
	return s.hiveRankWritable(ctx, userID, h)
}

// hiveRankWritable reports whether h ranks among userID's first
// FreeMaxHives hives account-wide by (created_at, id) - the hive-level
// half of isWritable, factored out so Update can call apiaries.Verify
// itself (to tell a locked parent apart from a locked hive - see
// ErrParentReadOnly vs ErrReadOnly) without this repeating that same
// call.
func (s *Service) hiveRankWritable(ctx context.Context, userID uuid.UUID, h *hive.Hive) (bool, error) {
	ids, err := s.hives.WritableIDs(ctx, userID, FreeMaxHives)
	if err != nil {
		return false, fmt.Errorf("hive: writable hive ids: %w", err)
	}
	for _, id := range ids {
		if id == h.ID {
			return true, nil
		}
	}
	return false, nil
}

// List returns the page of hives described by p, out of every hive
// belonging to userID. When search is non-nil its value is matched
// case-insensitively against the hive's name and notes fields. When
// sortOrder is non-nil ("asc" or "desc") the page is ordered by creation
// date in that direction instead of the repository's default order.
// When needsInspection is true, results are additionally restricted to
// hives that currently need inspection (see beebase-common/
// inspectionwarning) - accessToken is only ever used for that filter,
// forwarded to inspection-service so it can compute the answer against
// its own single configured threshold and inspection dates.
func (s *Service) List(ctx context.Context, userID uuid.UUID, accessToken string, p pagination.Params, search, sortOrder *string, needsInspection bool) ([]*WithAccess, int, error) {
	var needsInspectionIDs []uuid.UUID
	if needsInspection {
		ids, err := s.needsInspectionHiveIDs(ctx, userID, accessToken)
		if err != nil {
			return nil, 0, err
		}
		needsInspectionIDs = ids
	}

	hives, total, err := s.hives.ListByUser(ctx, userID, p, search, sortOrder, needsInspection, needsInspectionIDs)
	if err != nil {
		return nil, 0, err
	}

	annotate, err := s.newWritabilityCheck(ctx, userID, accessToken)
	if err != nil {
		return nil, 0, err
	}

	out := make([]*WithAccess, len(hives))
	for i, h := range hives {
		out[i] = &WithAccess{Hive: h, Writable: annotate(h)}
	}
	return out, total, nil
}

// newWritabilityCheck resolves, once, the caller's entitlement, their
// single writable apiary id, and (only if Free) userID's account-wide
// writable hive-id set, then returns a closure reporting per-hive
// writability with no further calls - used by List, whose hives may span
// several apiaries in one page, so this stays a single entitlement call,
// a single apiary-service call, and a single local query no matter the
// page size or how many distinct apiaries appear in it. The writable set
// always comes from the caller's complete live hives account-wide, never
// from the current page or search result, so which page or search term
// is being viewed can never change which hive the closure reports as
// writable.
func (s *Service) newWritabilityCheck(ctx context.Context, userID uuid.UUID, accessToken string) (func(*hive.Hive) bool, error) {
	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("hive: resolve entitlement: %w", err)
	}
	if entitlement != EntitlementFree {
		return func(*hive.Hive) bool { return true }, nil
	}

	writableApiaryID, unrestricted, err := s.apiaries.WritableApiaryID(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("hive: writable apiary id: %w", err)
	}
	if unrestricted {
		return func(*hive.Hive) bool { return true }, nil
	}
	if writableApiaryID == nil {
		return func(*hive.Hive) bool { return false }, nil
	}

	ids, err := s.hives.WritableIDs(ctx, userID, FreeMaxHives)
	if err != nil {
		return nil, fmt.Errorf("hive: writable hive ids: %w", err)
	}
	writableHives := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		writableHives[id] = true
	}

	return func(h *hive.Hive) bool {
		return h.ApiaryID == *writableApiaryID && writableHives[h.ID]
	}, nil
}

// ListByApiary returns the page of hives described by p belonging to
// userID under apiaryID, after confirming with apiary-service that userID
// actually owns that apiary. When search is non-nil its value is matched
// case-insensitively against the hive's name and notes fields. When
// sortOrder is non-nil ("asc" or "desc") the page is ordered by creation
// date in that direction instead of the repository's default order.
// needsInspection behaves exactly as in List.
func (s *Service) ListByApiary(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspection bool) ([]*WithAccess, int, error) {
	apiaryWritable, err := s.apiaries.Verify(ctx, accessToken, apiaryID)
	if err != nil {
		return nil, 0, err
	}

	var needsInspectionIDs []uuid.UUID
	if needsInspection {
		ids, err := s.needsInspectionHiveIDs(ctx, userID, accessToken)
		if err != nil {
			return nil, 0, err
		}
		needsInspectionIDs = ids
	}

	hives, total, err := s.hives.ListByApiary(ctx, userID, apiaryID, p, search, sortOrder, needsInspection, needsInspectionIDs)
	if err != nil {
		return nil, 0, err
	}

	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, 0, fmt.Errorf("hive: resolve entitlement: %w", err)
	}

	// apiaryID is already fixed (it's the path this method lists under),
	// and Verify above already resolved its writability - so, unlike
	// List, no second apiary-service call is needed here to learn which
	// apiary is writable. The hive-id ranking itself is still account-wide
	// (FreeMaxHives is a per-user quota, not per-apiary), so it's the same
	// query List's newWritabilityCheck runs, just inlined here since the
	// apiary half of the answer is already known.
	var writableHives map[uuid.UUID]bool
	if entitlement == EntitlementFree && apiaryWritable {
		ids, err := s.hives.WritableIDs(ctx, userID, FreeMaxHives)
		if err != nil {
			return nil, 0, fmt.Errorf("hive: writable hive ids: %w", err)
		}
		writableHives = make(map[uuid.UUID]bool, len(ids))
		for _, id := range ids {
			writableHives[id] = true
		}
	}

	out := make([]*WithAccess, len(hives))
	for i, h := range hives {
		writable := entitlement != EntitlementFree || (apiaryWritable && writableHives[h.ID])
		out[i] = &WithAccess{Hive: h, Writable: writable}
	}
	return out, total, nil
}

// needsInspectionHiveIDs returns the id of every hive userID owns that
// currently needs inspection: fetches userID's own hive ids (this
// service's own data) and inspection-service's hive-status (the latest
// InspectedAt per hive, plus the configured threshold), then applies the
// shared inspectionwarning.NeedsInspection rule to each - a hive absent
// from inspection-service's map has never been inspected, and so always
// needs inspection regardless of the threshold.
func (s *Service) needsInspectionHiveIDs(ctx context.Context, userID uuid.UUID, accessToken string) ([]uuid.UUID, error) {
	allHiveIDs, err := s.hives.ListIDsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("hive: list hive ids: %w", err)
	}

	latestByHive, thresholdDays, err := s.inspectionStatus.HiveInspectionStatus(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("hive: get inspection status: %w", err)
	}

	now := time.Now().UTC()
	needing := make([]uuid.UUID, 0, len(allHiveIDs))
	for _, id := range allHiveIDs {
		var latest *time.Time
		if t, ok := latestByHive[id]; ok {
			latest = &t
		}
		if inspectionwarning.NeedsInspection(latest, thresholdDays, now) {
			needing = append(needing, id)
		}
	}
	return needing, nil
}

// ApiaryIDsWithHives returns the id of every apiary userID owns at least
// one non-deleted hive under. Backs GET
// /api/v1/hives/apiary-ids-with-hives, which apiary-service calls to
// filter its own apiary listings to "apiaries without hives" - it has
// no notion of hives of its own.
func (s *Service) ApiaryIDsWithHives(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.hives.DistinctApiaryIDsWithHives(ctx, userID)
}

// Update replaces the editable fields of the hive identified by hiveID,
// if it belongs to userID, and returns the resulting hive. accessToken is
// the caller's own access token, forwarded to media-service so it can run
// its own ownership check. When in.Images is non-nil, it's deduplicated
// (preserving first-seen order) and, if non-empty, every id's ownership
// is verified against media-service before anything changes; if
// verification fails, Update returns the error immediately, leaving the
// hive's row (including its current Images) completely untouched. On
// success, Images is simply replaced with the deduplicated set - there is
// nothing external to reconcile against, since hive-service's own Images
// column is already the sole source of truth for what's referenced. When
// in.Images is nil, Images is left untouched entirely.
func (s *Service) Update(ctx context.Context, userID uuid.UUID, accessToken string, hiveID uuid.UUID, in UpdateInput) (*WithAccess, error) {
	h, err := s.hives.GetByID(ctx, userID, hiveID)
	if err != nil {
		return nil, err
	}

	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("hive: resolve entitlement: %w", err)
	}
	if entitlement == EntitlementFree {
		apiaryWritable, err := s.apiaries.Verify(ctx, accessToken, h.ApiaryID)
		if err != nil {
			return nil, err
		}
		if !apiaryWritable {
			return nil, ErrParentReadOnly
		}
		writable, err := s.hiveRankWritable(ctx, userID, h)
		if err != nil {
			return nil, err
		}
		if !writable {
			return nil, ErrReadOnly
		}
	}

	if in.Images != nil {
		dedup := dedupeImages(*in.Images)
		if len(dedup) > MaxMediaAttachments {
			return nil, ErrMediaLimitReached
		}
		if len(dedup) > 0 {
			if err := s.media.VerifyOwnership(ctx, accessToken, dedup); err != nil {
				return nil, err
			}
		}
		h.Images = dedup
	}

	h.Name = in.Name
	h.Notes = in.Notes
	h.UpdatedAt = time.Now().UTC()

	if err := s.hives.Update(ctx, h); err != nil {
		return nil, fmt.Errorf("hive: update: %w", err)
	}

	// The write above only ever succeeds when h was just proven writable
	// (Pro, or the Free-writable checks above), so the result is always
	// writable too.
	return &WithAccess{Hive: h, Writable: true}, nil
}

// dedupeImages returns ids with duplicates removed, preserving the order
// each id first appeared in - so a client submitting the same id twice
// can't cause redundant work or a spurious count mismatch against
// media-service's response.
func dedupeImages(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	dedup := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		dedup = append(dedup, id)
	}
	return dedup
}

// Delete cascades: every inspection belonging to hiveID is deleted first,
// then every media file this hive itself references (h.Images) is
// hard-deleted via media-service, then the hive itself is hard-deleted.
// accessToken is the caller's own access token, forwarded to
// inspection-service and media-service so each can run its own ownership
// check. If any step fails, Delete stops and returns the error without
// rolling back steps that already succeeded - there is no distributed
// transaction across these services, by design.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID, accessToken string, hiveID uuid.UUID) error {
	h, err := s.hives.GetByID(ctx, userID, hiveID)
	if err != nil {
		return err
	}
	return s.deleteCascade(ctx, userID, accessToken, h)
}

// DeleteByApiary cascades every hive under apiaryID, in-process (no
// self-HTTP-call): for each hive it runs the identical cascade Delete
// uses. It stops at the first hive that fails, leaving hives already
// fully deleted earlier in the loop deleted - the same no-rollback
// contract as Delete, just applied across a batch.
func (s *Service) DeleteByApiary(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID) error {
	hives, err := s.hives.ListAllByApiary(ctx, userID, apiaryID)
	if err != nil {
		return fmt.Errorf("hive: list by apiary: %w", err)
	}

	for _, h := range hives {
		if err := s.deleteCascade(ctx, userID, accessToken, h); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) DeleteLocalByUser(ctx context.Context, userID uuid.UUID) error {
	r, ok := s.hives.(interface {
		DeleteAllByUserHard(context.Context, uuid.UUID) error
	})
	if !ok {
		return fmt.Errorf("hive: repository does not support account cleanup")
	}
	return r.DeleteAllByUserHard(ctx, userID)
}

func (s *Service) deleteCascade(ctx context.Context, userID uuid.UUID, accessToken string, h *hive.Hive) error {
	if err := s.inspections.DeleteByHive(ctx, accessToken, h.ID); err != nil {
		return err
	}
	if len(h.Images) > 0 {
		if err := s.media.DeleteByIDs(ctx, accessToken, h.Images); err != nil {
			return err
		}
	}
	if s.harvests != nil {
		if err := s.harvests.DeleteByHive(ctx, accessToken, h.ID); err != nil {
			return err
		}
	}
	if s.reminders != nil {
		if err := s.reminders.Cleanup(ctx, "hive", h.ID); err != nil {
			return err
		}
	}
	return s.hives.HardDelete(ctx, userID, h.ID)
}
