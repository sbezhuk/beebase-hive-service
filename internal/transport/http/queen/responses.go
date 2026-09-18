package queen

import (
	"time"

	"github.com/google/uuid"

	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// Response is the public representation of a queen. Year is read-only output:
// it is always derived from MarkedAt, never accepted as input.
type Response struct {
	ID                uuid.UUID  `json:"id"`
	HiveID            uuid.UUID  `json:"hiveId"`
	Year              int        `json:"year"`
	MarkingColor      string     `json:"markingColor"`
	MarkingColorHex   string     `json:"markingColorHex"`
	MarkedAt          time.Time  `json:"markedAt"`
	IntroducedAt      time.Time  `json:"introducedAt"`
	RemovedAt         *time.Time `json:"removedAt"`
	ReplacementReason *string    `json:"replacementReason"`
	Notes             string     `json:"notes"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// NewResponse maps domain queen to public HTTP response.
func NewResponse(q *domainqueen.Queen) Response {
	var replacementReason *string
	if q.ReplacementReason != nil {
		s := string(*q.ReplacementReason)
		replacementReason = &s
	}
	return Response{
		ID:                q.ID,
		HiveID:            q.HiveID,
		Year:              q.Year(),
		MarkingColor:      string(q.MarkingColor()),
		MarkingColorHex:   q.MarkingColorHex(),
		MarkedAt:          q.MarkedAt,
		IntroducedAt:      q.IntroducedAt,
		RemovedAt:         q.RemovedAt,
		ReplacementReason: replacementReason,
		Notes:             q.Notes,
		CreatedAt:         q.CreatedAt,
		UpdatedAt:         q.UpdatedAt,
	}
}

// NewListResponse maps a slice of domain queens to a slice of public HTTP responses.
func NewListResponse(queens []*domainqueen.Queen) []Response {
	out := make([]Response, len(queens))
	for i, q := range queens {
		out[i] = NewResponse(q)
	}
	return out
}
