package hive

import "errors"

// ErrNotFound is returned both when no hive matches the given ID and when
// it exists but belongs to a different user. The two cases are
// deliberately indistinguishable to a caller: a user must never be able
// to tell whether another user's hive ID exists at all.
var ErrNotFound = errors.New("hive not found")
