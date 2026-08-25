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
