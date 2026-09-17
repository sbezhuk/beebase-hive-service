package queen

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the port through which the application persists and retrieves queens.
// Every method takes the parent hiveID alongside queen parameters, so scoping is
// guaranteed by the query itself.
type Repository interface {
	// InsertInChain atomically inserts a queen into the hive's strict chronological chain,
	// adjusting neighbor removed_at boundaries as needed.
	// If a queen with the same introduced_at already exists in the hive, returns ErrDuplicateIntroducedAt.
	InsertInChain(ctx context.Context, q *Queen) error

	// GetCurrentByHiveID returns the currently active queen for hiveID (removed_at IS NULL).
	// If none exists, returns ErrNotFound.
	GetCurrentByHiveID(ctx context.Context, hiveID uuid.UUID) (*Queen, error)

	// GetByID returns the queen identified by queenID under hiveID.
	// If not found, returns ErrNotFound.
	GetByID(ctx context.Context, hiveID, queenID uuid.UUID) (*Queen, error)

	// ListHistoryByHiveID returns all queens associated with hiveID, ordered
	// newest first (introduced_at DESC, created_at DESC).
	ListHistoryByHiveID(ctx context.Context, hiveID uuid.UUID) ([]*Queen, error)

	// UpdateInChain atomically updates metadata and introduced_at for an existing queen,
	// validating neighbor bounds and updating the predecessor's removed_at if introduced_at changes.
	UpdateInChain(ctx context.Context, hiveID, queenID uuid.UUID, year int, markedAt *time.Time, introducedAt time.Time, notes string) (*Queen, error)

	// DeleteLatest atomically hard-deletes the latest queen in the chain.
	// Returns ErrQueenNotLatest if the queen is not the latest.
	// If a predecessor exists, rolls it back to current by setting removed_at = NULL.
	DeleteLatest(ctx context.Context, hiveID, queenID uuid.UUID) error
}
