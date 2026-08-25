package hive

import (
	"context"

	"github.com/google/uuid"
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
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*Hive, error)
	// Update persists h.Name, h.Location, h.Notes, and h.UpdatedAt for the
	// hive identified by h.ID, scoped to h.UserID. ApiaryID is immutable
	// and never updated.
	Update(ctx context.Context, h *Hive) error
	// Delete soft-deletes the hive (sets deleted_at) rather than removing
	// the row, per the project's synchronizable-entity plan.
	Delete(ctx context.Context, userID, hiveID uuid.UUID) error
}
