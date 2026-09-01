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
	GetByID(ctx context.Context, userID, hiveID uuid.UUID) (*Hive, error)
	// ListByUser returns the page of hives described by p, along with the
	// total number of hives userID owns (independent of p, for computing
	// pagination metadata).
	ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params) (hives []*Hive, total int, err error)
	// Update persists h.Name, h.Location, h.Notes, and h.UpdatedAt for the
	// hive identified by h.ID, scoped to h.UserID. ApiaryID is immutable
	// and never updated.
	Update(ctx context.Context, h *Hive) error
	// ListByApiary returns every hive under apiaryID belonging to userID,
	// including ones a prior soft-delete already marked gone (deliberately
	// not filtered by deleted_at). Used only to drive DeleteByApiary's
	// cascade, which needs to find and purge every remaining artifact
	// under an apiary being deleted - not a user-facing list endpoint, so
	// it's unpaginated.
	ListByApiary(ctx context.Context, userID, apiaryID uuid.UUID) ([]*Hive, error)
	// HardDelete physically removes the hive row. There is no soft-delete
	// path left on this port: a hive delete is always a full cascade (see
	// application/hive.Service.Delete), called only after
	// inspection-service and media-service have already deleted
	// everything that belonged to this hive.
	HardDelete(ctx context.Context, userID, hiveID uuid.UUID) error
}
