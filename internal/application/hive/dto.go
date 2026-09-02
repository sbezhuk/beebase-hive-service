package hive

import "github.com/google/uuid"

// CreateInput is the input to Service.Create.
type CreateInput struct {
	ApiaryID uuid.UUID
	Name     string
	Notes    string
}

// UpdateInput is the input to Service.Update. Update replaces all three
// editable fields (PUT semantics), not a partial patch. ApiaryID isn't
// here: a hive can't be moved to a different apiary. Images is likewise
// left alone when nil so a caller that doesn't mention images at all
// can't accidentally detach every photo on an unrelated field edit.
type UpdateInput struct {
	Name  string
	Notes string
	// Images is the desired final set of media IDs attached to this
	// hive - each one either already attached here, or the caller's own
	// not-yet-attached upload (media-service links it on the fly). Nil
	// means "leave attached media alone"; a non-nil slice (including an
	// empty one) replaces the attached set exactly, attaching whatever's
	// newly listed and detaching whatever isn't listed.
	Images *[]uuid.UUID
}
