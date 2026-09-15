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

// ErrHiveLimitReached is returned when a free-tier user attempts to create
// more hives than permitted by the free plan across all their apiaries.
var ErrHiveLimitReached = errors.New("hive limit reached")

// ErrMediaLimitReached is returned when an attempt is made to attach more
// photos than permitted by the media attachment limit.
var ErrMediaLimitReached = errors.New("media limit reached")

// ErrReadOnly is returned when a free-tier user attempts to modify a hive
// that itself currently falls outside their Free entitlement (see
// FreeMaxHives and Service.isWritable) - i.e. a write attempted against a
// Pro-locked hive whose parent apiary is otherwise writable.
var ErrReadOnly = errors.New("hive is read-only under the free plan")

// ErrParentReadOnly is returned when a free-tier user attempts to modify a
// hive (or create one) under an apiary that itself currently falls outside
// their Free entitlement - distinct from ErrReadOnly so callers (and the
// API error contract) can tell "this hive itself needs Pro" apart from
// "its parent apiary needs Pro".
var ErrParentReadOnly = errors.New("parent apiary is read-only under the free plan")
