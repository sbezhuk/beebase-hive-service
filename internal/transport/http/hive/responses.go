package hive

import (
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// Response is the public representation of a hive.
type Response struct {
	ID        uuid.UUID `json:"id"`
	ApiaryID  uuid.UUID `json:"apiary_id"`
	Name      string    `json:"name"`
	Location  string    `json:"location"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newResponse(h *hive.Hive) Response {
	return Response{
		ID:        h.ID,
		ApiaryID:  h.ApiaryID,
		Name:      h.Name,
		Location:  h.Location,
		Notes:     h.Notes,
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
