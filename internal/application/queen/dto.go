package queen

import (
	"time"

	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// CreateInput holds the parameters for registering a queen into the hive's chain.
// There is no Year field: the marking year is always derived from MarkedAt.
type CreateInput struct {
	MarkedAt          time.Time
	IntroducedAt      time.Time
	ReplacementReason *domainqueen.ReplacementReason
	Notes             string
}

// UpdateInput holds the editable metadata fields for an existing queen.
// There is no Year field: the marking year is always derived from MarkedAt.
type UpdateInput struct {
	MarkedAt             time.Time
	IntroducedAt         time.Time
	ReplacementReason    *domainqueen.ReplacementReason
	HasReplacementReason bool
	Notes                string
}
