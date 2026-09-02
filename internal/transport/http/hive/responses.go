package hive

import (
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// Response is the public representation of a hive.
type Response struct {
	ID        uuid.UUID   `json:"id"`
	ApiaryID  uuid.UUID   `json:"apiary_id"`
	Name      string      `json:"name"`
	Notes     string      `json:"notes"`
	Images    []uuid.UUID `json:"images"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// newResponse builds a Response for h, with images as the IDs of media
// currently attached to it. A nil images (e.g. a freshly created hive, or
// a list item that deliberately skips the media-service round trip)
// renders as "images": [] rather than null.
func newResponse(h *hive.Hive, images []uuid.UUID) Response {
	if images == nil {
		images = []uuid.UUID{}
	}
	return Response{
		ID:        h.ID,
		ApiaryID:  h.ApiaryID,
		Name:      h.Name,
		Notes:     h.Notes,
		Images:    images,
		CreatedAt: h.CreatedAt,
		UpdatedAt: h.UpdatedAt,
	}
}

// newListResponse deliberately omits each item's attached media: fetching
// it would mean one media-service round trip per hive in the page (up to
// MaxLimit), an N+1 fan-out this endpoint doesn't pay. Clients that need
// images for a listed hive can fetch it directly via GET /hives/{id}, or
// query media-service's own list-by-owner endpoint.
func newListResponse(hives []*hive.Hive) []Response {
	out := make([]Response, len(hives))
	for i, h := range hives {
		out[i] = newResponse(h, nil)
	}
	return out
}
