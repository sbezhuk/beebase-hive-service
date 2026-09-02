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

// newResponse builds a Response for h. Images is read straight from h -
// never nil (Hive.Images is always a real, possibly-empty slice) - so it
// renders as "images": [] rather than null when there are no photos.
func newResponse(h *hive.Hive) Response {
	images := h.Images
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

func newListResponse(hives []*hive.Hive) []Response {
	out := make([]Response, len(hives))
	for i, h := range hives {
		out[i] = newResponse(h)
	}
	return out
}
