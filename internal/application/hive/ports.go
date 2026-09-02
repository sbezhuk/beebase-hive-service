package hive

import (
	"context"

	"github.com/google/uuid"
)

// ApiaryVerifier confirms that an apiary belongs to whoever presented
// accessToken. It's a port because apiaries live in a different service
// (with its own database); this service never queries apiary ownership
// itself, it only ever asks apiary-service.
type ApiaryVerifier interface {
	Verify(ctx context.Context, accessToken string, apiaryID uuid.UUID) error
}

// InspectionDeleter deletes every inspection belonging to a hive, in
// inspection-service, as part of cascading a hive delete. It's a port for
// the same reason ApiaryVerifier is: inspections live in a different
// service with its own database.
type InspectionDeleter interface {
	DeleteByHive(ctx context.Context, accessToken string, hiveID uuid.UUID) error
}

// MediaClient is hive-service's dependency on media-service. media-service
// has no notion of apiaries or hives at all - it only knows which files
// belong to which uploader - so hive-service is fully self-sufficient for
// "what's attached to this hive" (see Hive.Images, its own local column
// and the sole source of truth for reads); this client exists purely to
// verify a caller's ownership of newly-referenced media ids before
// persisting them, and to hard-delete a hive's files when the hive itself
// is cascade-deleted.
type MediaClient interface {
	// VerifyOwnership confirms every id in ids belongs to whoever
	// presented accessToken, by asking media-service directly - it's the
	// only remaining source of truth for "does this media id exist and
	// belong to me". Returns ErrImageNotFound if any id doesn't (unknown,
	// deleted, or someone else's - indistinguishable, by the same
	// non-leaking convention hive.ErrNotFound already follows).
	VerifyOwnership(ctx context.Context, accessToken string, ids []uuid.UUID) error
	// DeleteByIDs hard-deletes every media item in ids, used when the
	// hive itself is being cascade-deleted.
	DeleteByIDs(ctx context.Context, accessToken string, ids []uuid.UUID) error
}
