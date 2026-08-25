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
// here: a hive can't be moved to a different apiary.
type UpdateInput struct {
	Name     string
	Location string
	Notes    string
}
