package hive

import "errors"

// ErrApiaryNotFound is returned when the apiary a hive is being created
// under doesn't exist, doesn't belong to the caller, or its ownership
// couldn't be confirmed. As with hive.ErrNotFound, these cases are
// deliberately indistinguishable: a caller must not be able to tell
// whether another user's apiary ID exists at all.
var ErrApiaryNotFound = errors.New("apiary not found")

// ErrImageNotFound is returned when an ID in CreateInput.Images or
// UpdateInput.Images doesn't belong to the caller, verified via a read
// against media-service (GET /api/v1/media?ids=) - whether because it
// doesn't exist, was deleted, or belongs to a different user, without
// distinguishing why, by the same non-leaking convention hive.ErrNotFound
// already follows.
var ErrImageNotFound = errors.New("image not found")
