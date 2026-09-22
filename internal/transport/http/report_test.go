package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	appreport "github.com/sbezhuk/beebase-hive-service/internal/application/report"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	httptransport "github.com/sbezhuk/beebase-hive-service/internal/transport/http"
	hivehttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/hive"
)

func TestReportEndpointSuccessReturnsInlinePDF(t *testing.T) {
	hiveID := uuid.New()
	h := hivehttp.NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "", fakeReportService{model: &appreport.HiveReport{}}, fakeRenderer{content: []byte("%PDF-1.7\n%%EOF")})
	router := httptransport.NewRouter(slog.Default(), nil, h, fakeAccessParser{}, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hives/"+hiveID.String()+"/report?from=2026-01-01&to=2026-12-31&locale=uk", nil)
	req.Header.Set("Authorization", "Bearer access")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `inline; filename="hive-report-2026-01-01_2026-12-31.pdf"` {
		t.Fatalf("content disposition = %q", got)
	}
}

func TestReportEndpointMapsApplicationErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "pro", err: appreport.ErrProRequired, wantStatus: http.StatusForbidden, wantCode: "resource_pro_locked"},
		{name: "unavailable", err: appreport.ErrReportGenerationUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "report_generation_unavailable"},
		{name: "hive not found", err: domainhive.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "hive_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := hivehttp.NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "", fakeReportService{err: test.err}, fakeRenderer{})
			router := httptransport.NewRouter(slog.Default(), nil, h, fakeAccessParser{}, "")
			req := httptest.NewRequest(http.MethodGet, "/api/v1/hives/"+uuid.NewString()+"/report?from=2026-01-01&to=2026-12-31&locale=en", nil)
			req.Header.Set("Authorization", "Bearer access")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := errorCode(rec.Body.Bytes()); got != test.wantCode {
				t.Fatalf("error code = %q, want %q", got, test.wantCode)
			}
		})
	}
}

func TestReportEndpointValidationAndRendererFailure(t *testing.T) {
	h := hivehttp.NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "", fakeReportService{}, fakeRenderer{err: errors.New("renderer internals")})
	router := httptransport.NewRouter(slog.Default(), nil, h, fakeAccessParser{}, "")
	for _, raw := range []string{
		"/api/v1/hives/" + uuid.NewString() + "/report?from=2026-01-01&to=2027-01-02&locale=en",
		"/api/v1/hives/" + uuid.NewString() + "/report?from=2026-01-01&to=2026-01-02&locale=de",
	} {
		req := httptest.NewRequest(http.MethodGet, raw, nil)
		req.Header.Set("Authorization", "Bearer access")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d for %s", rec.Code, raw)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hives/"+uuid.NewString()+"/report?from=2026-01-01&to=2026-01-02&locale=en", nil)
	req.Header.Set("Authorization", "Bearer access")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || errorCode(rec.Body.Bytes()) != "report_generation_failed" {
		t.Fatalf("renderer failure = %d %s", rec.Code, rec.Body.String())
	}
}

func TestReportEndpointRequiresExistingAuthMiddleware(t *testing.T) {
	h := hivehttp.NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "", fakeReportService{}, fakeRenderer{})
	router := httptransport.NewRouter(slog.Default(), nil, h, fakeAccessParser{}, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hives/"+uuid.NewString()+"/report?from=2026-01-01&to=2026-01-02&locale=en", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

type fakeReportService struct {
	model *appreport.HiveReport
	err   error
}

func (f fakeReportService) Assemble(context.Context, uuid.UUID, string, uuid.UUID, appreport.Period, string) (*appreport.HiveReport, error) {
	return f.model, f.err
}

type fakeRenderer struct {
	content []byte
	err     error
}

func (f fakeRenderer) Render(context.Context, *appreport.HiveReport) ([]byte, error) {
	return f.content, f.err
}

type fakeAccessParser struct{}

func (fakeAccessParser) Parse(context.Context, string) (uuid.UUID, error) { return uuid.New(), nil }

func errorCode(body []byte) string {
	var value struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &value)
	return value.Error.Code
}
