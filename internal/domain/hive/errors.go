package hive

import "errors"

// ErrNotFound is returned both when no hive matches the given ID and when
// it exists but belongs to a different user. The two cases are
// deliberately indistinguishable to a caller: a user must never be able
// to tell whether another user's hive ID exists at all.
var ErrNotFound = errors.New("hive not found")

// ErrLimitReached is returned by Repository.CreateWithLimit when the user's
// active hive count across all apiaries already meets or exceeds the specified limit.
var ErrLimitReached = errors.New("hive limit reached")

// ErrNameTaken is returned when an active hive with the same name already
// exists under the same apiary.
var ErrNameTaken = errors.New("hive name taken")
