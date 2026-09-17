package queen

import (
	"context"
	"errors"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

var (
	// ErrInvalidYear is returned when a provided year is outside supported bounds.
	ErrInvalidYear = errors.New("queen year must be a valid 4-digit year")

	// ErrIntroducedAtRequired is returned when introduced_at is empty or zero.
	ErrIntroducedAtRequired = errors.New("introduced_at is required")

	// ErrTimelineInvalid is returned when introduced_at violates chain bounds.
	ErrTimelineInvalid = domainqueen.ErrTimelineInvalid

	// ErrHiveNotFound indicates the hive does not exist, was deleted, or belongs to another user.
	ErrHiveNotFound = domainhive.ErrNotFound

	// ErrQueenNotFound indicates the queen does not exist or does not belong to the specified hive.
	ErrQueenNotFound = domainqueen.ErrNotFound

	// ErrActiveQueenExists is returned when an active queen already exists on the hive.
	ErrActiveQueenExists = domainqueen.ErrActiveQueenExists

	// ErrQueenNotLatest is returned when attempting to delete a queen that is not latest in the chain.
	ErrQueenNotLatest = domainqueen.ErrQueenNotLatest

	// ErrDuplicateIntroducedAt is returned when a queen with the same introduction timestamp already exists.
	ErrDuplicateIntroducedAt = domainqueen.ErrDuplicateIntroducedAt

	// ErrReadOnly indicates the hive is currently read-only for the caller under their Free entitlement.
	ErrReadOnly = apphive.ErrReadOnly

	// ErrParentReadOnly indicates the hive's parent apiary is currently read-only for the caller.
	ErrParentReadOnly = apphive.ErrParentReadOnly
)

// HiveRepository is the port for querying hives and checking ownership.
type HiveRepository interface {
	GetByID(ctx context.Context, userID, hiveID uuid.UUID) (*domainhive.Hive, error)
	WritableIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error)
}

// QueenRepository is the port for persisting and querying queen records.
type QueenRepository interface {
	domainqueen.Repository
}

// ApiaryVerifier verifies apiary writability.
type ApiaryVerifier interface {
	Verify(ctx context.Context, accessToken string, apiaryID uuid.UUID) (writable bool, err error)
}

// EntitlementResolver resolves subscription entitlements.
type EntitlementResolver interface {
	GetEntitlement(ctx context.Context, accessToken string) (string, error)
}
