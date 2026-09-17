package queen

import (
	"time"

	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// CreateInput holds the parameters for registering a queen into the hive's chain.
type CreateInput struct {
	Year              int
	MarkedAt          *time.Time
	IntroducedAt      time.Time
	ReplacementReason *domainqueen.ReplacementReason
	Notes             string
}

// UpdateInput holds the editable metadata fields for an existing queen.
type UpdateInput struct {
	Year                 int
	MarkedAt             *time.Time
	IntroducedAt         time.Time
	ReplacementReason    *domainqueen.ReplacementReason
	HasReplacementReason bool
	Notes                string
}
