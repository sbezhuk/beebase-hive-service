// Package hive holds the HTTP handlers for hive management. Handlers stay
// thin: they decode/validate the request, pull the authenticated user's ID
// (and, for Create, their raw access token, forwarded to apiary-service)
// from the request, call into the application service, and map the
// result (or error) to a response. No business logic or repository
// access happens here.
package hive

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	httpmw "github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/httpx"
	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// Error codes for hive failures, returned as the top-level "error.code".
// Each is a stable key a client can map to a localized message.
// CodeApiaryNotFound intentionally reuses apiary-service's own code
// string, since it's the same meaning from the client's point of view
// regardless of which service returned it.
const (
	CodeHiveNotFound   = "hive_not_found"
	CodeInvalidHiveID  = "invalid_hive_id"
	CodeApiaryNotFound = "apiary_not_found"
)

// Handler exposes the hive HTTP endpoints. Every method requires the
// request to have already passed through httpmw.RequireAuth.
type Handler struct {
	service *apphive.Service
	log     *slog.Logger
}

// NewHandler returns a Handler backed by service.
func NewHandler(service *apphive.Service, log *slog.Logger) *Handler {
	return &Handler{service: service, log: log}
}

// Create handles POST /hives.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	var req CreateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	// Already validated as a well-formed UUID by CreateRequest.Validate.
	apiaryID, _ := uuid.Parse(req.ApiaryID)

	created, err := h.service.Create(r.Context(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     req.Name,
		Location: req.Location,
		Notes:    req.Notes,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, newResponse(created))
}

// List handles GET /hives.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hives, err := h.service.List(r.Context(), userID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newListResponse(hives))
}

// Get handles GET /hives/{hiveID}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	got, err := h.service.Get(r.Context(), userID, hiveID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newResponse(got))
}

// Update handles PUT /hives/{hiveID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	var req UpdateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	updated, err := h.service.Update(r.Context(), userID, hiveID, apphive.UpdateInput{
		Name:     req.Name,
		Location: req.Location,
		Notes:    req.Notes,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newResponse(updated))
}

// Delete handles DELETE /hives/{hiveID}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	if err := h.service.Delete(r.Context(), userID, hiveID); err != nil {
		h.writeServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// requireAuth returns the authenticated user's ID (from context, set by
// httpmw.RequireAuth) and their raw access token (read back off the
// request's own Authorization header, which RequireAuth already
// validated) so it can be forwarded to apiary-service.
func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	userID, ok := httpmw.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpmw.CodeMissingAuthorization, "missing authentication")
		return uuid.Nil, "", false
	}

	const prefix = "Bearer "
	token := strings.TrimPrefix(r.Header.Get("Authorization"), prefix)

	return userID, token, true
}

func (h *Handler) pathHiveID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "hiveID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, CodeInvalidHiveID, "hive id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hive.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, CodeHiveNotFound, "hive not found")
	case errors.Is(err, apphive.ErrApiaryNotFound):
		httpx.WriteError(w, http.StatusNotFound, CodeApiaryNotFound, "apiary not found")
	default:
		httpx.WriteInternalError(w, h.log, err)
	}
}
