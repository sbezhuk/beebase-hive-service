package queen

import (
	"time"
)

// CreateInput holds the parameters for registering a queen into the hive's chain.
type CreateInput struct {
	Year         int
	MarkedAt     *time.Time
	IntroducedAt time.Time
	Notes        string
}

// UpdateInput holds the editable metadata fields for an existing queen.
type UpdateInput struct {
	Year         int
	MarkedAt     *time.Time
	IntroducedAt time.Time
	Notes        string
}
