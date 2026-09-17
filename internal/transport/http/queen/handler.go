package queen

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
	appqueen "github.com/sbezhuk/beebase-hive-service/internal/application/queen"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// Error codes for queen endpoints.
const (
	CodeHiveNotFound            = "hive_not_found"
	CodeInvalidHiveID           = "invalid_hive_id"
	CodeQueenNotFound           = "queen_not_found"
	CodeInvalidQueenID          = "invalid_queen_id"
	CodeActiveQueenExists       = "active_queen_exists"
	CodeQueenNotLatest          = "queen_not_latest"
	CodeDuplicateIntroducedAt   = "duplicate_introduced_at"
	CodeResourceProLocked       = "resource_pro_locked"
	CodeParentResourceProLocked = "parent_resource_pro_locked"
)

// Handler exposes the queen HTTP endpoints.
type Handler struct {
	service *appqueen.Service
	log     *slog.Logger
}

// NewHandler constructs a Handler backed by queen service.
func NewHandler(service *appqueen.Service, log *slog.Logger) *Handler {
	return &Handler{
		service: service,
		log:     log,
	}
}

// Create handles POST /api/v1/hives/{hiveId}/queens.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	var req CreateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	created, err := h.service.Create(r.Context(), userID, token, hiveID, appqueen.CreateInput{
		Year:         req.Year,
		MarkedAt:     req.MarkedAt,
		IntroducedAt: *req.IntroducedAt,
		Notes:        req.Notes,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, NewResponse(created))
}

// GetCurrent handles GET /api/v1/hives/{hiveId}/queen.
func (h *Handler) GetCurrent(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	current, err := h.service.GetCurrent(r.Context(), userID, hiveID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, NewResponse(current))
}

// GetByID handles GET /api/v1/hives/{hiveId}/queens/{queenId}.
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	queenID, ok := h.pathQueenID(w, r)
	if !ok {
		return
	}

	q, err := h.service.GetByID(r.Context(), userID, hiveID, queenID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, NewResponse(q))
}

// ListHistory handles GET /api/v1/hives/{hiveId}/queens.
func (h *Handler) ListHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	queens, err := h.service.ListHistory(r.Context(), userID, hiveID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, NewListResponse(queens))
}

// Update handles PUT /api/v1/hives/{hiveId}/queens/{queenId}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	queenID, ok := h.pathQueenID(w, r)
	if !ok {
		return
	}

	var req UpdateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	updated, err := h.service.Update(r.Context(), userID, token, hiveID, queenID, appqueen.UpdateInput{
		Year:         req.Year,
		MarkedAt:     req.MarkedAt,
		IntroducedAt: *req.IntroducedAt,
		Notes:        req.Notes,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, NewResponse(updated))
}

// Delete handles DELETE /api/v1/hives/{hiveId}/queens/{queenId}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	queenID, ok := h.pathQueenID(w, r)
	if !ok {
		return
	}

	if err := h.service.Delete(r.Context(), userID, token, hiveID, queenID); err != nil {
		h.writeServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return uuid.Nil, "", false
	}

	const prefix = "Bearer "
	token := strings.TrimPrefix(r.Header.Get("Authorization"), prefix)
	return userID, token, true
}

func (h *Handler) requireUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	userID, ok := httpmw.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid user context")
		return uuid.Nil, false
	}
	return userID, true
}

func (h *Handler) pathHiveID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "hiveId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, CodeInvalidHiveID, "hive id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) pathQueenID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "queenId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, CodeInvalidQueenID, "queen id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domainhive.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, CodeHiveNotFound, "hive not found")
	case errors.Is(err, domainqueen.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, CodeQueenNotFound, "queen not found")
	case errors.Is(err, domainqueen.ErrQueenNotLatest):
		httpx.WriteError(w, http.StatusConflict, CodeQueenNotLatest, "only the latest queen in the chain can be deleted")
	case errors.Is(err, domainqueen.ErrDuplicateIntroducedAt):
		httpx.WriteError(w, http.StatusConflict, CodeDuplicateIntroducedAt, "a queen with this introduction timestamp already exists in this hive")
	case errors.Is(err, domainqueen.ErrActiveQueenExists):
		httpx.WriteError(w, http.StatusConflict, CodeActiveQueenExists, "hive already has an active queen")
	case errors.Is(err, appqueen.ErrInvalidYear):
		httpx.WriteValidationError(w, map[string]string{"year": CodeYearInvalid})
	case errors.Is(err, appqueen.ErrIntroducedAtRequired):
		httpx.WriteValidationError(w, map[string]string{"introducedAt": CodeIntroducedAtRequired})
	case errors.Is(err, appqueen.ErrTimelineInvalid):
		httpx.WriteValidationError(w, map[string]string{"introducedAt": CodeTimelineInvalid})
	case errors.Is(err, apphive.ErrReadOnly):
		httpx.WriteError(w, http.StatusForbidden, CodeResourceProLocked, "this hive requires Pro to edit")
	case errors.Is(err, apphive.ErrParentReadOnly):
		httpx.WriteError(w, http.StatusForbidden, CodeParentResourceProLocked, "this hive's apiary requires Pro to edit")
	default:
		httpx.WriteInternalError(w, h.log, err)
	}
}
