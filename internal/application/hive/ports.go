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

// MediaClient is hive-service's dependency on media-service: deleting
// every media item attached to a hive (as part of cascading a hive
// delete), and reconciling which media stay attached to a hive on
// update.
type MediaClient interface {
	// DeleteByOwner deletes every media item attached to a hive.
	DeleteByOwner(ctx context.Context, accessToken string, hiveID uuid.UUID) error
	// ListAttached returns the IDs of every media item currently
	// attached to hiveID, belonging to whoever presented accessToken.
	ListAttached(ctx context.Context, accessToken string, hiveID uuid.UUID) ([]uuid.UUID, error)
	// VerifyAttached confirms mediaID exists, belongs to whoever
	// presented accessToken, and is already attached to hiveID. Returns
	// ErrImageNotFound otherwise.
	VerifyAttached(ctx context.Context, accessToken string, hiveID, mediaID uuid.UUID) error
	// Detach removes a single media item, used to drop images an update
	// no longer wants attached to this hive.
	Detach(ctx context.Context, accessToken string, mediaID uuid.UUID) error
}
