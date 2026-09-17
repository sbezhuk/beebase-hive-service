package queen

import (
	"time"

	"github.com/google/uuid"

	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// Response is the public representation of a queen.
type Response struct {
	ID              uuid.UUID  `json:"id"`
	HiveID          uuid.UUID  `json:"hiveId"`
	Year            int        `json:"year"`
	MarkingColor    string     `json:"markingColor"`
	MarkingColorHex string     `json:"markingColorHex"`
	MarkedAt        *time.Time `json:"markedAt"`
	IntroducedAt    time.Time  `json:"introducedAt"`
	RemovedAt       *time.Time `json:"removedAt"`
	Notes           string     `json:"notes"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// NewResponse maps domain queen to public HTTP response.
func NewResponse(q *domainqueen.Queen) Response {
	return Response{
		ID:              q.ID,
		HiveID:          q.HiveID,
		Year:            q.Year,
		MarkingColor:    string(q.MarkingColor()),
		MarkingColorHex: q.MarkingColorHex(),
		MarkedAt:        q.MarkedAt,
		IntroducedAt:    q.IntroducedAt,
		RemovedAt:       q.RemovedAt,
		Notes:           q.Notes,
		CreatedAt:       q.CreatedAt,
		UpdatedAt:       q.UpdatedAt,
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
