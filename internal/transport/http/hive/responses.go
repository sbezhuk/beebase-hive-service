package hive

import (
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/medialink"
	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
)

// ImageResponse is the public representation of one image attached to a
// hive: its media id, plus the URL a client loads/caches it from. The
// URL is derived, not stored - it's always media-service's stable
// download route, built fresh on every response.
type ImageResponse struct {
	ID       uuid.UUID `json:"id"`
	ImageURL string    `json:"imageUrl"`
}

// Response is the public representation of a hive.
type Response struct {
	ID       uuid.UUID       `json:"id"`
	ApiaryID uuid.UUID       `json:"apiaryId"`
	Name     string          `json:"name"`
	Notes    string          `json:"notes"`
	Images   []ImageResponse `json:"images"`
	// Writable reports whether the caller can currently edit this hive,
	// create inspections/harvests under it, or otherwise mutate it or its
	// descendants. Always true under Pro; under Free, true only when its
	// parent apiary is itself writable and it ranks among the caller's
	// first FreeMaxHives hives in that apiary (see application/hive.
	// Service.isWritable). Lets Flutter (and inspection-service/
	// harvest-service) render/enforce locked-resource behavior without
	// reimplementing this selection themselves.
	Writable  bool      `json:"writable"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// newResponse builds a Response for h. Images is read straight from h -
// never nil (Hive.Images is always a real, possibly-empty slice) - so it
// renders as "images": [] rather than null when there are no photos.
func newResponse(h *apphive.WithAccess, publicBaseURL string) Response {
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
		Writable:  h.Writable,
		CreatedAt: h.CreatedAt,
		UpdatedAt: h.UpdatedAt,
	}
}

func newListResponse(hives []*apphive.WithAccess, publicBaseURL string) []Response {
	out := make([]Response, len(hives))
	for i, h := range hives {
		out[i] = newResponse(h, publicBaseURL)
	}
	return out
}

// ApiaryIDsWithHivesResponse is the public representation of GET
// /api/v1/hives/apiary-ids-with-hives: every apiary id the caller owns
// at least one non-deleted hive under.
type ApiaryIDsWithHivesResponse struct {
	ApiaryIDs []uuid.UUID `json:"apiaryIds"`
}

// newApiaryIDsWithHivesResponse builds an ApiaryIDsWithHivesResponse.
// ApiaryIDs is never nil, so it renders as "[]" rather than "null" when
// the caller has no hives at all.
func newApiaryIDsWithHivesResponse(ids []uuid.UUID) ApiaryIDsWithHivesResponse {
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return ApiaryIDsWithHivesResponse{ApiaryIDs: ids}
}
