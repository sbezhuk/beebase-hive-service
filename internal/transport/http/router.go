// Package http wires the HTTP transport: routing, middleware, and the
// handlers that don't yet belong to a specific domain (health, readiness).
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	httpmw "github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/httpx"
	"github.com/sbezhuk/beebase-common/internalauth"
	hivehttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/hive"
	queenhttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/queen"
)

// NewRouter builds the root HTTP handler for the service.
func NewRouter(
	log *slog.Logger,
	db *pgxpool.Pool,
	hiveHandler *hivehttp.Handler,
	tokenParser httpmw.AccessTokenParser,
	extras ...any,
) http.Handler {
	internalToken := ""
	var queenHandler *queenhttp.Handler
	for _, extra := range extras {
		switch v := extra.(type) {
		case string:
			internalToken = v
		case *queenhttp.Handler:
			queenHandler = v
		}
	}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(requestLogger(log))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/health", HealthHandler)
	r.Get("/ready", ReadyHandler(db))
	r.With(internalauth.RequireAuth(internalToken)).Get("/internal/api/v1/hives/{id}/exists", existsHandler(db, "hives", true))
	r.With(internalauth.RequireAuth(internalToken)).Get("/internal/api/v1/hives/{id}/owner", ownerHandler(db))
	r.With(internalauth.RequireAuth(internalToken)).Delete("/internal/api/v1/users/{userID}", func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "userID"))
		if err != nil {
			httpx.WriteError(w, 400, "invalid_user_id", "invalid user id")
			return
		}
		if err := hiveHandler.DeleteUserData(req.Context(), id); err != nil {
			httpx.WriteError(w, 500, "cleanup_failed", "could not delete hive data")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	r.Group(func(r chi.Router) {
		r.Use(httpmw.RequireAuth(tokenParser))

		r.Route("/api/v1/hives", func(r chi.Router) {
			r.Post("/", hiveHandler.Create)
			r.Get("/", hiveHandler.List)
			// Internal-only: called by apiary-service to filter its own
			// apiary listings to "apiaries without hives", never directly
			// by an end-user client. Registered as a static sibling of
			// "/{hiveID}" rather than under it, so it can never be
			// confused with a hive id.
			r.Get("/apiary-ids-with-hives", hiveHandler.ApiaryIDsWithHives)
			r.Get("/{hiveId}", hiveHandler.Get)
			r.Put("/{hiveId}", hiveHandler.Update)
			r.Delete("/{hiveId}", hiveHandler.Delete)
			// Internal-only: called by apiary-service when it deletes an
			// apiary, forwarding the caller's own access token. This route
			// group's RequireAuth can't distinguish that from a genuine
			// end-user call - beebase-gateway is what actually blocks external
			// reachability, by never proxying this exact method+path.
			r.Delete("/", hiveHandler.DeleteByApiary)

			if queenHandler != nil {
				r.Get("/{hiveId}/queen", queenHandler.GetCurrent)
				r.Route("/{hiveId}/queens", func(r chi.Router) {
					r.Get("/", queenHandler.ListHistory)
					r.Post("/", queenHandler.Create)
					r.Get("/{queenId}", queenHandler.GetByID)
					r.Put("/{queenId}", queenHandler.Update)
					r.Delete("/{queenId}", queenHandler.Delete)
				})
			}
		})

		r.Get("/api/v1/apiaries/{apiaryId}/hives", hiveHandler.ListByApiary)
	})

	return r
}

func ownerHandler(db *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var owner uuid.UUID
		if err = db.QueryRow(r.Context(), `SELECT user_id FROM hives WHERE id=$1`, id).Scan(&owner); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			UserID uuid.UUID `json:"userId"`
		}{owner})
	}
}
func existsHandler(db *pgxpool.Pool, table string, soft bool) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, e := uuid.Parse(chi.URLParam(r, "id"))
		if e != nil {
			http.NotFound(w, r)
			return
		}
		q := "SELECT EXISTS(SELECT 1 FROM " + table + " WHERE id=$1"
		if soft {
			q += " AND deleted_at IS NULL"
		}
		q += ")"
		var ok bool
		if e = db.QueryRow(r.Context(), q, id).Scan(&ok); e != nil {
			http.Error(w, "", 500)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// requestLogger logs each request's method, path, status, and duration
// through slog instead of chi's default stdlib logger.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}
