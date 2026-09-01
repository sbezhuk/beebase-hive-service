package hive

import "errors"

// ErrApiaryNotFound is returned when the apiary a hive is being created
// under doesn't exist, doesn't belong to the caller, or its ownership
// couldn't be confirmed. As with hive.ErrNotFound, these cases are
// deliberately indistinguishable: a caller must not be able to tell
// whether another user's apiary ID exists at all.
var ErrApiaryNotFound = errors.New("apiary not found")

// ErrImageNotFound is returned when an ID in UpdateInput.Images doesn't
// identify a media item already attached to this hive - whether because
// it doesn't exist, belongs to a different user, or is attached to a
// different owner entirely. A media item's owner is fixed at upload time
// in media-service and can't be moved, so the only IDs an update can ever
// legitimately keep are ones already attached to this same hive; anything
// else is rejected without distinguishing why, by the same non-leaking
// convention hive.ErrNotFound already follows.
var ErrImageNotFound = errors.New("image not found")
