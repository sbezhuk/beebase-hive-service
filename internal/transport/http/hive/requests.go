package hive

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/httpx"
)

const (
	maxNameLength  = 200
	maxNotesLength = 2000
)

// Field validation error codes. Each is a stable key a client can map to a
// localized message; the field carrying no error is simply absent from the
// response's "fields" map.
const (
	CodeApiaryIDRequired = "apiary_id_required"
	CodeApiaryIDInvalid  = "apiary_id_invalid"
	CodeNameRequired     = "name_required"
	CodeNameTooLong      = "name_too_long"
	CodeNotesTooLong     = "notes_too_long"
	CodeImagesInvalid    = "images_invalid"
)

// validatable is implemented by every request DTO in this package.
// Validate returns a map of field name to error code, empty if valid.
type validatable interface {
	Validate() map[string]string
}

// decodeAndValidate decodes the request body into dst and validates it,
// writing an appropriate error response and returning false if either step
// fails.
func decodeAndValidate(w http.ResponseWriter, r *http.Request, dst validatable) bool {
	defer func() { _ = r.Body.Close() }()

	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidBody, "request body must be valid JSON")
		return false
	}

	if fields := dst.Validate(); len(fields) > 0 {
		httpx.WriteValidationError(w, fields)
		return false
	}

	return true
}

// CreateRequest is the body of POST /hives.
type CreateRequest struct {
	ApiaryID string `json:"apiary_id"`
	Name     string `json:"name"`
	Notes    string `json:"notes"`
}

func (r *CreateRequest) Validate() map[string]string {
	fields := validateFields(r.Name, r.Notes)

	switch {
	case strings.TrimSpace(r.ApiaryID) == "":
		fields["apiary_id"] = CodeApiaryIDRequired
	default:
		if _, err := uuid.Parse(r.ApiaryID); err != nil {
			fields["apiary_id"] = CodeApiaryIDInvalid
		}
	}

	return fields
}

// UpdateRequest is the body of PUT /hives/{hiveID}. Update replaces both
// editable fields (PUT semantics), not a partial patch. There's no
// apiary_id here: a hive can't be moved to a different apiary.
type UpdateRequest struct {
	Name  string `json:"name"`
	Notes string `json:"notes"`
	// Images, when present (even as an empty array), is the desired
	// final set of already-uploaded media IDs attached to this hive;
	// omitting the field (or sending JSON null) leaves currently
	// attached media untouched. Go's json package already distinguishes
	// "absent/null" (nil slice) from "[]" (non-nil, empty slice), which
	// is exactly the distinction this needs.
	Images []string `json:"images"`
}

func (r *UpdateRequest) Validate() map[string]string {
	fields := validateFields(r.Name, r.Notes)

	for _, id := range r.Images {
		if _, err := uuid.Parse(id); err != nil {
			fields["images"] = CodeImagesInvalid
			break
		}
	}

	return fields
}

func validateFields(name, notes string) map[string]string {
	fields := map[string]string{}

	switch {
	case strings.TrimSpace(name) == "":
		fields["name"] = CodeNameRequired
	case len(name) > maxNameLength:
		fields["name"] = CodeNameTooLong
	}

	if len(notes) > maxNotesLength {
		fields["notes"] = CodeNotesTooLong
	}

	return fields
}
