package hive

import "errors"

// ErrApiaryNotFound is returned when the apiary a hive is being created
// under doesn't exist, doesn't belong to the caller, or its ownership
// couldn't be confirmed. As with hive.ErrNotFound, these cases are
// deliberately indistinguishable: a caller must not be able to tell
// whether another user's apiary ID exists at all.
var ErrApiaryNotFound = errors.New("apiary not found")
