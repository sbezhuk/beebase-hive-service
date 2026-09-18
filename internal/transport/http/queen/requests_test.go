package queen

import (
	"encoding/json"
	"testing"
	"time"
)

func TestQueenRequestsRejectIntroducedAtAfterUTCToday(t *testing.T) {
	today := time.Now().UTC()
	tomorrow := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, time.UTC)
	markedAt := today.Format(time.RFC3339)
	introducedAt := tomorrow.Format(time.RFC3339)
	payload := []byte(`{"markedAt":"` + markedAt + `","introducedAt":"` + introducedAt + `","notes":"complete"}`)

	var create CreateRequest
	if err := json.Unmarshal(payload, &create); err != nil {
		t.Fatal(err)
	}
	if got := create.Validate()["introducedAt"]; got != CodeIntroducedAtInFuture {
		t.Fatalf("Create introducedAt validation = %q, want %q", got, CodeIntroducedAtInFuture)
	}

	var update UpdateRequest
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatal(err)
	}
	if got := update.Validate()["introducedAt"]; got != CodeIntroducedAtInFuture {
		t.Fatalf("Update introducedAt validation = %q, want %q", got, CodeIntroducedAtInFuture)
	}
}
