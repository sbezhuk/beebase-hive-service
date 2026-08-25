// Package hive holds the Hive entity and the port through which the rest
// of the application persists and retrieves it. It has no dependency on
// HTTP, PostgreSQL, or any other infrastructure concern.
package hive

import (
	"time"

	"github.com/google/uuid"
)

// Hive is a beekeeper's registered hive, belonging to exactly one apiary.
// It is a synchronizable entity (UUID, created_at, updated_at,
// deleted_at) per the project's offline-sync plan, even though full sync
// isn't implemented yet.
type Hive struct {
	ID       uuid.UUID
	UserID   uuid.UUID // denormalized owner; see Repository doc comment
	ApiaryID uuid.UUID // immutable after creation
	Name     string
	Location string
	Notes    string

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// New constructs a Hive owned by userID under apiaryID, with a freshly
// generated ID and timestamps set to now. Callers must have already
// verified that apiaryID belongs to userID before calling New.
func New(userID, apiaryID uuid.UUID, name, location, notes string) *Hive {
	now := time.Now().UTC()
	return &Hive{
		ID:        uuid.New(),
		UserID:    userID,
		ApiaryID:  apiaryID,
		Name:      name,
		Location:  location,
		Notes:     notes,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
