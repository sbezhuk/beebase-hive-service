package hive

import (
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/medialink"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// ImageResponse is the public representation of one image attached to a
// hive: its media id, plus the URL a client loads/caches it from. The
// URL is derived, not stored - it's always media-service's stable
// download route, built fresh on every response.
type ImageResponse struct {
	ID       uuid.UUID `json:"id"`
	ImageURL string    `json:"image_url"`
}

// Response is the public representation of a hive.
type Response struct {
	ID        uuid.UUID       `json:"id"`
	ApiaryID  uuid.UUID       `json:"apiary_id"`
	Name      string          `json:"name"`
	Notes     string          `json:"notes"`
	Images    []ImageResponse `json:"images"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// newResponse builds a Response for h. Images is read straight from h -
// never nil (Hive.Images is always a real, possibly-empty slice) - so it
// renders as "images": [] rather than null when there are no photos.
func newResponse(h *hive.Hive, publicBaseURL string) Response {
	images := make([]ImageResponse, len(h.Images))
	for i, id := range h.Images {
		images[i] = ImageResponse{ID: id, ImageURL: medialink.DownloadURL(publicBaseURL, id)}
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

func newListResponse(hives []*hive.Hive, publicBaseURL string) []Response {
	out := make([]Response, len(hives))
	for i, h := range hives {
		out[i] = newResponse(h, publicBaseURL)
	}
	return out
}
