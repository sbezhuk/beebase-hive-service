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

// MediaDeleter deletes every media item attached to a hive, in
// media-service, as part of cascading a hive delete.
type MediaDeleter interface {
	DeleteByOwner(ctx context.Context, accessToken string, hiveID uuid.UUID) error
}
