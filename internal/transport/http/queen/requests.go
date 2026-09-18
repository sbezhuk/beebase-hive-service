package queen

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/sbezhuk/beebase-common/httpx"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

const maxNotesLength = 2000

// Validation error codes for queen requests.
const (
	CodeMarkedAtRequired            = "marked_at_required"
	CodeIntroducedAtRequired        = "introduced_at_required"
	CodeIntroducedAtInFuture        = "introduced_at_in_future"
	CodeNotesTooLong                = "notes_too_long"
	CodeTimelineInvalid             = "timeline_invalid"
	CodeReplacementReasonInvalid    = "replacement_reason_invalid"
	CodeReplacementReasonNotAllowed = "replacement_reason_not_allowed"
)

type validatable interface {
	Validate() map[string]string
}

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

// CreateRequest is the request body for POST /api/v1/hives/{hiveId}/queens.
// There is no year field: the marking year is always derived from markedAt.
type CreateRequest struct {
	MarkedAt          *time.Time `json:"markedAt"`
	IntroducedAt      *time.Time `json:"introducedAt"`
	ReplacementReason *string    `json:"replacementReason"`
	Notes             string     `json:"notes"`
}

func (r *CreateRequest) Validate() map[string]string {
	fields := map[string]string{}
	if r.MarkedAt == nil || r.MarkedAt.IsZero() {
		fields["markedAt"] = CodeMarkedAtRequired
	}
	if r.IntroducedAt == nil || r.IntroducedAt.IsZero() {
		fields["introducedAt"] = CodeIntroducedAtRequired
	} else if domainqueen.IsFutureCalendarDate(*r.IntroducedAt) {
		fields["introducedAt"] = CodeIntroducedAtInFuture
	}
	if r.ReplacementReason != nil && !domainqueen.ReplacementReason(*r.ReplacementReason).IsValid() {
		fields["replacementReason"] = CodeReplacementReasonInvalid
	}
	if len(r.Notes) > maxNotesLength {
		fields["notes"] = CodeNotesTooLong
	}
	return fields
}

// UpdateRequest is the request body for PUT /api/v1/hives/{hiveId}/queens/{queenId}.
// There is no year field: the marking year is always derived from markedAt.
type UpdateRequest struct {
	MarkedAt             *time.Time `json:"markedAt"`
	IntroducedAt         *time.Time `json:"introducedAt"`
	ReplacementReason    *string    `json:"replacementReason"`
	HasReplacementReason bool       `json:"-"`
	Notes                string     `json:"notes"`
}

func (r *UpdateRequest) UnmarshalJSON(data []byte) error {
	type Alias UpdateRequest
	aux := struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	_, r.HasReplacementReason = raw["replacementReason"]
	return nil
}

func (r *UpdateRequest) Validate() map[string]string {
	fields := map[string]string{}
	if r.MarkedAt == nil || r.MarkedAt.IsZero() {
		fields["markedAt"] = CodeMarkedAtRequired
	}
	if r.IntroducedAt == nil || r.IntroducedAt.IsZero() {
		fields["introducedAt"] = CodeIntroducedAtRequired
	} else if domainqueen.IsFutureCalendarDate(*r.IntroducedAt) {
		fields["introducedAt"] = CodeIntroducedAtInFuture
	}
	if r.HasReplacementReason && r.ReplacementReason != nil && !domainqueen.ReplacementReason(*r.ReplacementReason).IsValid() {
		fields["replacementReason"] = CodeReplacementReasonInvalid
	}
	if len(r.Notes) > maxNotesLength {
		fields["notes"] = CodeNotesTooLong
	}
	return fields
}
