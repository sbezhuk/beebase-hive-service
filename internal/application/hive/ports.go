package hive

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// ApiaryVerifier confirms that an apiary belongs to whoever presented
// accessToken, and resolves apiary-level Free/Pro writability. It's a port
// because apiaries live in a different service (with its own database);
// this service never queries apiary ownership or apiary entitlement
// itself, it only ever asks apiary-service - the sole source of truth for
// both, since apiary ordering/ownership data lives only there.
type ApiaryVerifier interface {
	// Verify confirms apiaryID belongs to whoever presented accessToken,
	// and reports whether apiary-service currently considers it writable
	// (always true under Pro; under Free, true only for the one apiary
	// within the caller's entitlement). Returns ErrApiaryNotFound if it
	// doesn't belong to them (or doesn't exist).
	Verify(ctx context.Context, accessToken string, apiaryID uuid.UUID) (writable bool, err error)
	// WritableApiaryID returns the id of the one apiary within the
	// caller's Free entitlement (nil if they own none), or reports
	// unrestricted=true when they currently have Pro - meaning every
	// apiary they own is writable and ApiaryID is meaningless. Used to
	// annotate hive list/get responses in bulk, without a per-row or
	// per-apiary-id round trip to apiary-service.
	WritableApiaryID(ctx context.Context, accessToken string) (apiaryID *uuid.UUID, unrestricted bool, err error)
}

// InspectionDeleter deletes every inspection belonging to a hive, in
// inspection-service, as part of cascading a hive delete. It's a port for
// the same reason ApiaryVerifier is: inspections live in a different
// service with its own database.
type InspectionDeleter interface {
	DeleteByHive(ctx context.Context, accessToken string, hiveID uuid.UUID) error
}

type HarvestDeleter interface {
	DeleteByHive(ctx context.Context, accessToken string, hiveID uuid.UUID) error
}

type EntityCleanup interface {
	Cleanup(ctx context.Context, entityType string, entityID uuid.UUID) error
}

// QueenProvider provides the currently active queen for a hive, if any.
type QueenProvider interface {
	GetCurrentByHiveID(ctx context.Context, hiveID uuid.UUID) (*queen.Queen, error)
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

// InspectionStatusProvider is this service's dependency on
// inspection-service, used only when a hive listing is filtered to
// "needs inspection". It's the single source of truth for both the
// warning threshold and the raw inspection dates - this service never
// maintains its own copy of either, so its filter always agrees with
// statistics-service's Dashboard metric, which reads the same endpoint.
type InspectionStatusProvider interface {
	// HiveInspectionStatus returns, for whoever presented accessToken,
	// the latest InspectedAt for every hive they've ever inspected (a
	// hive absent from the map has never been inspected), and the
	// currently configured inspection warning threshold in days.
	HiveInspectionStatus(ctx context.Context, accessToken string) (latestByHive map[uuid.UUID]time.Time, thresholdDays int, err error)
}

// Entitlement values returned by subscription-service.
const (
	EntitlementFree = "free"
	EntitlementPro  = "pro"

	// FreeMaxHives is the maximum number of hives a free-tier user can own across all apiaries.
	FreeMaxHives = 5

	// MaxMediaAttachments is the maximum number of media attachments allowed per hive.
	MaxMediaAttachments = 5
)

// EntitlementResolver resolves the subscription entitlement for a user by
// forwarding their access token to subscription-service.
type EntitlementResolver interface {
	GetEntitlement(ctx context.Context, accessToken string) (string, error)
}
