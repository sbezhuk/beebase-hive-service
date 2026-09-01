package hive

import "github.com/google/uuid"

// CreateInput is the input to Service.Create.
type CreateInput struct {
	ApiaryID uuid.UUID
	Name     string
	Location string
	Notes    string
}

// UpdateInput is the input to Service.Update. Update replaces all three
// editable fields (PUT semantics), not a partial patch. ApiaryID isn't
// here: a hive can't be moved to a different apiary. Images is likewise
// left alone when nil so a caller that doesn't mention images at all
// can't accidentally detach every photo on an unrelated field edit.
type UpdateInput struct {
	Name     string
	Location string
	Notes    string
	// Images is the desired final set of already-uploaded media IDs
	// attached to this hive. Nil means "leave attached media alone"; a
	// non-nil slice (including an empty one) replaces the attached set
	// exactly, detaching whatever isn't listed.
	Images *[]uuid.UUID
}
