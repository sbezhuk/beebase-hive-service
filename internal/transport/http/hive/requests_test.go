package hive

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCreateRequest_Validate(t *testing.T) {
	validApiaryID := uuid.New().String()

	tests := []struct {
		name string
		req  CreateRequest
		want map[string]string
	}{
		{
			name: "valid",
			req:  CreateRequest{ApiaryID: validApiaryID, Name: "Hive 1", Notes: "n/a"},
			want: map[string]string{},
		},
		{
			name: "missing apiary_id",
			req:  CreateRequest{ApiaryID: "", Name: "Hive 1"},
			want: map[string]string{"apiary_id": CodeApiaryIDRequired},
		},
		{
			name: "malformed apiary_id",
			req:  CreateRequest{ApiaryID: "not-a-uuid", Name: "Hive 1"},
			want: map[string]string{"apiary_id": CodeApiaryIDInvalid},
		},
		{
			name: "empty name",
			req:  CreateRequest{ApiaryID: validApiaryID, Name: ""},
			want: map[string]string{"name": CodeNameRequired},
		},
		{
			name: "name too long",
			req:  CreateRequest{ApiaryID: validApiaryID, Name: strings.Repeat("a", maxNameLength+1)},
			want: map[string]string{"name": CodeNameTooLong},
		},
		{
			name: "notes too long",
			req:  CreateRequest{ApiaryID: validApiaryID, Name: "ok", Notes: strings.Repeat("a", maxNotesLength+1)},
			want: map[string]string{"notes": CodeNotesTooLong},
		},
		{
			name: "everything wrong at once",
			req:  CreateRequest{ApiaryID: "bad", Name: ""},
			want: map[string]string{"apiary_id": CodeApiaryIDInvalid, "name": CodeNameRequired},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.req.Validate()
			if len(got) != len(tt.want) {
				t.Fatalf("Validate() = %v, want %v", got, tt.want)
			}
			for field, wantCode := range tt.want {
				if gotCode, ok := got[field]; !ok || gotCode != wantCode {
					t.Errorf("field %q: got code %q, want %q", field, gotCode, wantCode)
				}
			}
		})
	}
}

func TestCreateRequest_Validate_Images(t *testing.T) {
	validApiaryID := uuid.New().String()

	if fields := (&CreateRequest{ApiaryID: validApiaryID, Name: "ok", Images: nil}).Validate(); len(fields) != 0 {
		t.Errorf("nil images: expected no errors, got %v", fields)
	}
	if fields := (&CreateRequest{ApiaryID: validApiaryID, Name: "ok", Images: []string{}}).Validate(); len(fields) != 0 {
		t.Errorf("empty images: expected no errors, got %v", fields)
	}
	if fields := (&CreateRequest{ApiaryID: validApiaryID, Name: "ok", Images: []string{uuid.New().String()}}).Validate(); len(fields) != 0 {
		t.Errorf("valid image id: expected no errors, got %v", fields)
	}

	fields := (&CreateRequest{ApiaryID: validApiaryID, Name: "ok", Images: []string{"not-a-uuid"}}).Validate()
	if code := fields["images"]; code != CodeImagesInvalid {
		t.Errorf("images code = %q, want %q", code, CodeImagesInvalid)
	}
}

func TestUpdateRequest_Validate(t *testing.T) {
	if fields := (&UpdateRequest{Name: "ok"}).Validate(); len(fields) != 0 {
		t.Errorf("expected no errors, got %v", fields)
	}

	fields := (&UpdateRequest{Name: ""}).Validate()
	if code := fields["name"]; code != CodeNameRequired {
		t.Errorf("name code = %q, want %q", code, CodeNameRequired)
	}
}

func TestUpdateRequest_Validate_Images(t *testing.T) {
	if fields := (&UpdateRequest{Name: "ok", Images: nil}).Validate(); len(fields) != 0 {
		t.Errorf("nil images: expected no errors, got %v", fields)
	}
	if fields := (&UpdateRequest{Name: "ok", Images: []string{}}).Validate(); len(fields) != 0 {
		t.Errorf("empty images: expected no errors, got %v", fields)
	}
	if fields := (&UpdateRequest{Name: "ok", Images: []string{uuid.New().String()}}).Validate(); len(fields) != 0 {
		t.Errorf("valid image id: expected no errors, got %v", fields)
	}

	fields := (&UpdateRequest{Name: "ok", Images: []string{"not-a-uuid"}}).Validate()
	if code := fields["images"]; code != CodeImagesInvalid {
		t.Errorf("images code = %q, want %q", code, CodeImagesInvalid)
	}
}

func TestParseSearch(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name       string
		query      string
		wantSearch *string
		wantCode   string
	}{
		{
			name:       "omitted",
			query:      "",
			wantSearch: nil,
		},
		{
			name:       "empty",
			query:      "search=",
			wantSearch: nil,
		},
		{
			name:     "one char",
			query:    "search=a",
			wantCode: CodeInvalidSearch,
		},
		{
			name:     "two chars",
			query:    "search=ab",
			wantCode: CodeInvalidSearch,
		},
		{
			name:       "three chars",
			query:      "search=abc",
			wantSearch: strPtr("abc"),
		},
		{
			name:       "long search",
			query:      "search=hive%20notes",
			wantSearch: strPtr("hive notes"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			s, fields := parseSearch(req, nil)
			if tc.wantCode != "" {
				if fields["search"] != tc.wantCode {
					t.Fatalf("fields[search] = %q, want %q", fields["search"], tc.wantCode)
				}
				if s != nil {
					t.Fatalf("search = %v, want nil", s)
				}
			} else {
				if len(fields) != 0 {
					t.Fatalf("unexpected fields: %v", fields)
				}
				if tc.wantSearch == nil && s != nil {
					t.Fatalf("search = %v, want nil", s)
				}
				if tc.wantSearch != nil {
					if s == nil || *s != *tc.wantSearch {
						t.Fatalf("search = %v, want %v", s, *tc.wantSearch)
					}
				}
			}
		})
	}
}

func TestParseSortOrder(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name          string
		query         string
		wantSortOrder *string
		wantCode      string
	}{
		{
			name:          "omitted",
			query:         "",
			wantSortOrder: nil,
		},
		{
			name:          "asc",
			query:         "sortOrder=asc",
			wantSortOrder: strPtr("asc"),
		},
		{
			name:          "desc",
			query:         "sortOrder=desc",
			wantSortOrder: strPtr("desc"),
		},
		{
			name:     "invalid",
			query:    "sortOrder=newest",
			wantCode: CodeInvalidSortOrder,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			s, fields := parseSortOrder(req, nil)
			if tc.wantCode != "" {
				if fields["sortOrder"] != tc.wantCode {
					t.Fatalf("fields[sortOrder] = %q, want %q", fields["sortOrder"], tc.wantCode)
				}
				if s != nil {
					t.Fatalf("sortOrder = %v, want nil", s)
				}
				return
			}
			if len(fields) != 0 {
				t.Fatalf("unexpected fields: %v", fields)
			}
			if tc.wantSortOrder == nil && s != nil {
				t.Fatalf("sortOrder = %v, want nil", s)
			}
			if tc.wantSortOrder != nil {
				if s == nil || *s != *tc.wantSortOrder {
					t.Fatalf("sortOrder = %v, want %v", s, *tc.wantSortOrder)
				}
			}
		})
	}
}
