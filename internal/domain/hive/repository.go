package hive

import (
	"context"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/pagination"
)

// Repository is the port through which the application persists and
// retrieves hives. Every method that targets a specific hive takes the
// owning userID alongside the hive ID, so ownership is enforced by the
// query itself, not by a separate check layered on top.
//
// UserID is denormalized onto the hive row rather than looked up via
// ApiaryID on every call: apiary-service (a different service, a
// different database) is the only source of truth for apiary ownership,
// and is asked exactly once, at creation time. ApiaryID never changes
// after that, so the denormalized UserID stays correct without a
// cross-service call on every read.
type Repository interface {
	Create(ctx context.Context, h *Hive) error
	// CreateWithLimit creates a new hive, but only if the user owns fewer
	// than maxCount active hives across all apiaries. If maxCount <= 0, creation is unlimited.
	// Returns ErrLimitReached if the limit is exceeded.
	CreateWithLimit(ctx context.Context, h *Hive, maxCount int) error
	// CountByUser returns the total number of non-deleted hives owned by userID across all apiaries.
	CountByUser(ctx context.Context, userID uuid.UUID) (int, error)
	// CountByApiary returns the total number of non-deleted hives under
	// apiaryID. Used to enforce the Free hive limit, which the product
	// model scopes to hives within the one apiary a Free user may
	// currently create into (see application/hive.Service.Create) -
	// hives sitting under a different, currently-read-only apiary can
	// never occupy a Free slot, so they must never count against it.
	CountByApiary(ctx context.Context, apiaryID uuid.UUID) (int, error)
	// WritableIDs returns the ids of the oldest up to limit non-deleted
	// hives under apiaryID, ordered created_at ASC, id ASC - the
	// deterministic selection of which of that apiary's hives fall within
	// a Free user's writable-hive entitlement (see application/hive.
	// FreeMaxHives) when apiaryID is itself the caller's writable apiary.
	// Computed fresh from current live rows on every call, the same way
	// apiary-service's WritableIDs is - see that method's doc comment for
	// why. If limit <= 0, returns every hive id under apiaryID.
	WritableIDs(ctx context.Context, apiaryID uuid.UUID, limit int) ([]uuid.UUID, error)
	GetByID(ctx context.Context, userID, hiveID uuid.UUID) (*Hive, error)
	// ListByUser returns the page of hives described by p, along with the
	// total number of hives userID owns (independent of p, for computing
	// pagination metadata). When search is non-nil its value is matched
	// case-insensitively against name and notes; a nil search means no
	// filter. When sortOrder is non-nil ("asc" or "desc") the page is
	// ordered by creation date in that direction instead of the default
	// order; a nil sortOrder keeps the default order. When
	// needsInspectionOnly is true, results are additionally restricted to
	// hive ids in needsInspectionHiveIDs (computed by the application
	// layer from inspection-service's hive-status - this repository has
	// no notion of inspections of its own); an empty
	// needsInspectionHiveIDs then correctly yields zero rows, not "no
	// filter". When needsInspectionOnly is false, needsInspectionHiveIDs
	// is ignored.
	ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspectionOnly bool, needsInspectionHiveIDs []uuid.UUID) (hives []*Hive, total int, err error)
	// ListIDsByUser returns the id of every non-deleted hive userID owns,
	// unpaginated. Used only to build the "needs inspection" filter set
	// (see ListByUser) - never a user-facing list endpoint - so it's not
	// a user-facing list endpoint's usual page-of-results shape.
	ListIDsByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	// DistinctApiaryIDsWithHives returns the id of every apiary userID
	// owns at least one non-deleted hive under. Used by apiary-service
	// (via GET /api/v1/hives/apiary-ids-with-hives) to filter its own
	// apiary listings to "apiaries without hives" - apiary-service has no
	// notion of hives of its own, so it asks here instead of duplicating
	// this service's data.
	DistinctApiaryIDsWithHives(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	// Update persists h.Name, h.Notes, and h.UpdatedAt for the hive
	// identified by h.ID, scoped to h.UserID. ApiaryID is immutable and
	// never updated.
	Update(ctx context.Context, h *Hive) error
	// ListByApiary returns the page of hives described by p belonging to
	// userID under apiaryID, along with the total number of active hives
	// in that apiary. When search is non-nil its value is matched
	// case-insensitively against name and notes; a nil search means no
	// filter. When sortOrder is non-nil ("asc" or "desc") the page is
	// ordered by creation date in that direction instead of the default
	// order; a nil sortOrder keeps the default order. needsInspectionOnly
	// and needsInspectionHiveIDs behave exactly as in ListByUser.
	ListByApiary(ctx context.Context, userID, apiaryID uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspectionOnly bool, needsInspectionHiveIDs []uuid.UUID) (hives []*Hive, total int, err error)
	// ListAllByApiary returns every hive under apiaryID belonging to userID,
	// including ones a prior soft-delete already marked gone (deliberately
	// not filtered by deleted_at). Used only to drive DeleteByApiary's
	// cascade, which needs to find and purge every remaining artifact
	// under an apiary being deleted - not a user-facing list endpoint, so
	// it's unpaginated.
	ListAllByApiary(ctx context.Context, userID, apiaryID uuid.UUID) ([]*Hive, error)
	// HardDelete physically removes the hive row. There is no soft-delete
	// path left on this port: a hive delete is always a full cascade (see
	// application/hive.Service.Delete), called only after
	// inspection-service and media-service have already deleted
	// everything that belonged to this hive.
	HardDelete(ctx context.Context, userID, hiveID uuid.UUID) error
}
