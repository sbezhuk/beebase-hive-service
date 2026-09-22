// Package hive holds the HTTP handlers for hive management. Handlers stay
// thin: they decode/validate the request, pull the authenticated user's ID
// (and, for Create, their raw access token, forwarded to apiary-service)
// from the request, call into the application service, and map the
// result (or error) to a response. No business logic or repository
// access happens here.
package hive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	httpmw "github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/httpx"
	"github.com/sbezhuk/beebase-common/pagination"
	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	appreport "github.com/sbezhuk/beebase-hive-service/internal/application/report"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	reportpdf "github.com/sbezhuk/beebase-hive-service/internal/report/pdf"
)

// Error codes for hive failures, returned as the top-level "error.code".
// Each is a stable key a client can map to a localized message.
// CodeApiaryNotFound intentionally reuses apiary-service's own code
// string, since it's the same meaning from the client's point of view
// regardless of which service returned it.
const (
	CodeHiveNotFound      = "hive_not_found"
	CodeInvalidHiveID     = "invalid_hive_id"
	CodeApiaryNotFound    = "apiary_not_found"
	CodeInvalidApiaryID   = "invalid_apiary_id"
	CodeImageNotFound     = "image_not_found"
	CodeInvalidSearch     = "invalid_search"
	CodeInvalidSortOrder  = "invalid_sort_order"
	CodeHiveLimitReached  = "hive_limit_reached"
	CodeHiveNameExists    = "hive_name_exists"
	CodeMediaLimitReached = "media_limit_reached"
	// CodeResourceProLocked identifies a write attempted against a hive
	// that itself currently requires Pro. CodeParentResourceProLocked
	// identifies a write attempted against (or under) a hive whose parent
	// apiary currently requires Pro - distinct so a client can tell "this
	// hive needs Pro" apart from "its apiary needs Pro" without parsing
	// the message.
	CodeResourceProLocked       = "resource_pro_locked"
	CodeParentResourceProLocked = "parent_resource_pro_locked"
)

const minSearchLength = 3

// Handler exposes the hive HTTP endpoints. Every method requires the
// request to have already passed through httpmw.RequireAuth.
type Handler struct {
	service       *apphive.Service
	log           *slog.Logger
	publicBaseURL string
	reminders     interface {
		Cleanup(context.Context, string, uuid.UUID) error
	}
	reports interface {
		Assemble(context.Context, uuid.UUID, string, uuid.UUID, appreport.Period, string) (*appreport.HiveReport, error)
	}
	renderer interface {
		Render(context.Context, *appreport.HiveReport) ([]byte, error)
	}
}

func (h *Handler) DeleteUserData(ctx context.Context, userID uuid.UUID) error {
	return h.service.DeleteLocalByUser(ctx, userID)
}

// NewHandler returns a Handler backed by service. publicBaseURL is the
// gateway's externally reachable base URL, used to build each image's
// image_url.
func NewHandler(service *apphive.Service, log *slog.Logger, publicBaseURL string, extras ...any) *Handler {
	h := &Handler{service: service, log: log, publicBaseURL: publicBaseURL}
	for _, extra := range extras {
		switch value := extra.(type) {
		case interface {
			Cleanup(context.Context, string, uuid.UUID) error
		}:
			h.reminders = value
		case interface {
			Assemble(context.Context, uuid.UUID, string, uuid.UUID, appreport.Period, string) (*appreport.HiveReport, error)
		}:
			h.reports = value
		case interface {
			Render(context.Context, *appreport.HiveReport) ([]byte, error)
		}:
			h.renderer = value
		}
	}
	return h
}

// Report handles GET /api/v1/hives/{hiveId}/report. It only translates HTTP
// input and output; authorization, entitlement, and report assembly remain in
// the application service.
func (h *Handler) Report(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}
	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}
	period, locale, fields := parseReportInput(r)
	if len(fields) > 0 {
		httpx.WriteValidationError(w, fields)
		return
	}
	if h.reports == nil || h.renderer == nil {
		h.writeReportFailure(w, errors.New("report dependencies are not configured"))
		return
	}
	model, err := h.reports.Assemble(r.Context(), userID, token, hiveID, period, locale)
	if err != nil {
		h.writeReportServiceError(w, err)
		return
	}
	content, err := h.renderer.Render(r.Context(), model)
	if err != nil {
		h.writeReportFailure(w, err)
		return
	}
	filename := fmt.Sprintf("hive-report-%s_%s.pdf", period.From.Format("2006-01-02"), period.To.Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
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

	images := make([]uuid.UUID, len(req.Images))
	for i, s := range req.Images {
		images[i], _ = uuid.Parse(s) // already validated by req.Validate
	}

	created, err := h.service.Create(r.Context(), userID, token, apphive.CreateInput{
		ApiaryID: apiaryID,
		Name:     req.Name,
		Notes:    req.Notes,
		Images:   images,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, newResponse(created, h.publicBaseURL))
}

// List handles GET /hives.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	p, fields := pagination.ParseParams(r)
	search, fields := parseSearch(r, fields)
	sortOrder, fields := parseSortOrder(r, fields)
	if len(fields) > 0 {
		httpx.WriteValidationError(w, fields)
		return
	}
	needsInspection := parseNeedsInspection(r)

	hives, total, err := h.service.List(r.Context(), userID, token, p, search, sortOrder, needsInspection)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, pagination.NewResponse(newListResponse(hives, h.publicBaseURL), p, total))
}

// ListByApiary handles GET /api/v1/apiaries/{apiaryId}/hives.
func (h *Handler) ListByApiary(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	apiaryID, ok := h.pathApiaryID(w, r)
	if !ok {
		return
	}

	p, fields := pagination.ParseParams(r)
	search, fields := parseSearch(r, fields)
	sortOrder, fields := parseSortOrder(r, fields)
	if len(fields) > 0 {
		httpx.WriteValidationError(w, fields)
		return
	}
	needsInspection := parseNeedsInspection(r)

	hives, total, err := h.service.ListByApiary(r.Context(), userID, token, apiaryID, p, search, sortOrder, needsInspection)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, pagination.NewResponse(newListResponse(hives, h.publicBaseURL), p, total))
}

// parseNeedsInspection reads the optional "needs_inspection" query
// parameter: only the exact value "true" filters; anything else
// (absent, "false", or garbage) leaves results unfiltered - there's no
// invalid value to reject here, unlike search/sortOrder.
func parseNeedsInspection(r *http.Request) bool {
	return r.URL.Query().Get("needsInspection") == "true"
}

// ApiaryIDsWithHives handles GET /api/v1/hives/apiary-ids-with-hives.
// Called by apiary-service to filter its own apiary listings to
// "apiaries without hives", never directly by an end-user client.
func (h *Handler) ApiaryIDsWithHives(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return
	}

	ids, err := h.service.ApiaryIDsWithHives(r.Context(), userID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newApiaryIDsWithHivesResponse(ids))
}

func parseSearch(r *http.Request, fields map[string]string) (*string, map[string]string) {
	s := r.URL.Query().Get("search")
	if s == "" {
		return nil, fields
	}
	if len(s) < minSearchLength {
		if fields == nil {
			fields = map[string]string{}
		}
		fields["search"] = CodeInvalidSearch
		return nil, fields
	}
	return &s, fields
}

// parseSortOrder reads the optional "sortOrder" query parameter, which
// requests the list be ordered by creation date instead of the endpoint's
// default order. A missing value means "use the default order" (nil); an
// invalid value ("asc"/"desc" are the only accepted ones) is reported as a
// validation error the same way parseSearch reports one.
func parseSortOrder(r *http.Request, fields map[string]string) (*string, map[string]string) {
	s := r.URL.Query().Get("sortOrder")
	if s == "" {
		return nil, fields
	}
	if s != "asc" && s != "desc" {
		if fields == nil {
			fields = map[string]string{}
		}
		fields["sortOrder"] = CodeInvalidSortOrder
		return nil, fields
	}
	return &s, fields
}

// Get handles GET /hives/{hiveID}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	got, err := h.service.Get(r.Context(), userID, token, hiveID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newResponse(got, h.publicBaseURL))
}

// Update handles PUT /hives/{hiveID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
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

	var images *[]uuid.UUID
	if req.Images != nil {
		parsed := make([]uuid.UUID, len(req.Images))
		for i, s := range req.Images {
			parsed[i], _ = uuid.Parse(s) // already validated by req.Validate
		}
		images = &parsed
	}

	updated, err := h.service.Update(r.Context(), userID, token, hiveID, apphive.UpdateInput{
		Name:   req.Name,
		Notes:  req.Notes,
		Images: images,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, newResponse(updated, h.publicBaseURL))
}

// Delete handles DELETE /hives/{hiveID}. It cascades: every inspection and
// media item belonging to the hive is deleted first, then the hive
// itself.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	hiveID, ok := h.pathHiveID(w, r)
	if !ok {
		return
	}

	if err := h.service.Delete(r.Context(), userID, token, hiveID); err != nil {
		h.writeServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	if h.reminders != nil {
		if err := h.reminders.Cleanup(r.Context(), "hive", hiveID); err != nil {
			h.log.Warn("reminder cleanup failed", "entity_type", "hive", "entity_id", hiveID, "error", err)
		}
	}
}

// DeleteByApiary handles DELETE /hives?apiaryId=. It cascades every hive
// under the apiary (and, transitively, their inspections and media).
// Called by apiary-service when it deletes an apiary, forwarding the
// caller's own access token.
func (h *Handler) DeleteByApiary(w http.ResponseWriter, r *http.Request) {
	userID, token, ok := h.requireAuth(w, r)
	if !ok {
		return
	}

	raw := r.URL.Query().Get("apiaryId")
	apiaryID, err := uuid.Parse(raw)
	if raw == "" || err != nil {
		httpx.WriteError(w, http.StatusBadRequest, CodeInvalidApiaryID, "apiary_id must be a valid UUID")
		return
	}

	if err := h.service.DeleteByApiary(r.Context(), userID, token, apiaryID); err != nil {
		h.writeServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requireUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	userID, ok := httpmw.UserIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpmw.CodeMissingAuthorization, "missing authentication")
		return uuid.Nil, false
	}
	return userID, true
}

// requireAuth returns the authenticated user's ID alongside their raw
// access token (read back off the request's own Authorization header,
// which RequireAuth already validated) so it can be forwarded to
// apiary-service/media-service - to verify apiary ownership on Create, or
// when Create/Update ask media-service to verify ownership of
// newly-referenced images.
func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	userID, ok := h.requireUserID(w, r)
	if !ok {
		return uuid.Nil, "", false
	}

	const prefix = "Bearer "
	token := strings.TrimPrefix(r.Header.Get("Authorization"), prefix)

	return userID, token, true
}

func (h *Handler) pathHiveID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "hiveId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, CodeInvalidHiveID, "hive id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) pathApiaryID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "apiaryId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, CodeInvalidApiaryID, "apiary id must be a valid UUID")
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
	case errors.Is(err, apphive.ErrImageNotFound):
		httpx.WriteValidationError(w, map[string]string{"images": CodeImageNotFound})
	case errors.Is(err, apphive.ErrHiveLimitReached):
		httpx.WriteError(w, http.StatusForbidden, CodeHiveLimitReached, "free tier allows a maximum of 5 hives")
	case errors.Is(err, apphive.ErrReadOnly):
		httpx.WriteError(w, http.StatusForbidden, CodeResourceProLocked, "this hive requires Pro to edit")
	case errors.Is(err, apphive.ErrParentReadOnly):
		httpx.WriteError(w, http.StatusForbidden, CodeParentResourceProLocked, "this hive's apiary requires Pro to edit")
	case errors.Is(err, apphive.ErrMediaLimitReached):
		httpx.WriteError(w, http.StatusBadRequest, CodeMediaLimitReached, "maximum 5 photos allowed")
	case errors.Is(err, hive.ErrNameTaken):
		httpx.WriteError(w, http.StatusConflict, CodeHiveNameExists, "hive name already exists in this apiary")
	default:
		httpx.WriteInternalError(w, h.log, err)
	}
}

func parseReportInput(r *http.Request) (appreport.Period, string, map[string]string) {
	query := r.URL.Query()
	from, to, locale := query.Get("from"), query.Get("to"), query.Get("locale")
	fields := map[string]string{}
	if from == "" {
		fields["from"] = "from_required"
	}
	if to == "" {
		fields["to"] = "to_required"
	}
	if locale == "" {
		fields["locale"] = "locale_required"
	}
	if from != "" && to != "" {
		period, err := appreport.ParsePeriod(from, to)
		if err != nil {
			if from != "" {
				if _, parseErr := time.Parse("2006-01-02", from); parseErr != nil {
					fields["from"] = "from_invalid"
				}
			}
			if to != "" {
				if _, parseErr := time.Parse("2006-01-02", to); parseErr != nil {
					fields["to"] = "to_invalid"
				}
			}
			if len(fields) == 0 {
				if strings.Contains(err.Error(), "after") {
					fields["to"] = "from_after_to"
				} else {
					fields["to"] = "report_range_too_long"
				}
			}
			return appreport.Period{}, locale, fields
		} else if len(fields) == 0 {
			if err := appreport.ValidateLocale(locale); err != nil {
				fields["locale"] = "locale_invalid"
			}
			return period, locale, fields
		}
	}
	if locale != "" {
		if err := appreport.ValidateLocale(locale); err != nil {
			fields["locale"] = "locale_invalid"
		}
	}
	return appreport.Period{}, locale, fields
}

func (h *Handler) writeReportServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appreport.ErrProRequired):
		httpx.WriteError(w, http.StatusForbidden, CodeResourceProLocked, "Hive Report requires Pro")
	case errors.Is(err, appreport.ErrReportGenerationUnavailable):
		httpx.WriteError(w, http.StatusServiceUnavailable, "report_generation_unavailable", "report generation is temporarily unavailable")
	case errors.Is(err, hive.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, CodeHiveNotFound, "hive not found")
	default:
		h.writeReportFailure(w, err)
	}
}

func (h *Handler) writeReportFailure(w http.ResponseWriter, err error) {
	if h.log != nil {
		h.log.Error("hive report generation failed", "error", err)
	}
	code := "report_generation_failed"
	if errors.Is(err, reportpdf.ErrRender) {
		code = "report_generation_failed"
	}
	httpx.WriteError(w, http.StatusInternalServerError, code, "could not generate hive report")
}
