package hive

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

func TestWriteServiceError(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "")

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "hive not found",
			err:        hive.ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeHiveNotFound,
		},
		{
			name:       "apiary not found",
			err:        apphive.ErrApiaryNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeApiaryNotFound,
		},
		{
			name:       "hive limit reached",
			err:        apphive.ErrHiveLimitReached,
			wantStatus: http.StatusForbidden,
			wantCode:   CodeHiveLimitReached,
		},
		{
			name:       "hive name exists",
			err:        hive.ErrNameTaken,
			wantStatus: http.StatusConflict,
			wantCode:   CodeHiveNameExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.writeServiceError(rec, tt.err)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode json: %v", err)
			}

			if body.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}
		})
	}
}
