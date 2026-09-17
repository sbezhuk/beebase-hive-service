package queen

import "errors"

var (
	// ErrNotFound is returned when a requested queen does not exist or does not belong to the specified hive.
	ErrNotFound = errors.New("queen not found")

	// ErrActiveQueenExists is returned when attempting to register a new active queen for a hive
	// that already has an active queen, without performing a replacement.
	ErrActiveQueenExists = errors.New("hive already has an active queen")

	// ErrNoActiveQueen is returned when attempting to replace or remove the current queen
	// on a hive that has no active queen.
	ErrNoActiveQueen = errors.New("hive has no active queen")

	// ErrQueenNotLatest is returned when attempting to delete a queen that is not the latest in the chain.
	ErrQueenNotLatest = errors.New("only the latest queen in the chain can be deleted")

	// ErrDuplicateIntroducedAt is returned when a queen with the same introduction timestamp already exists in the hive.
	ErrDuplicateIntroducedAt = errors.New("a queen with this introduction timestamp already exists in this hive")

	// ErrTimelineInvalid is returned when introduced_at violates chain timeline bounds.
	ErrTimelineInvalid = errors.New("introduced_at violates chain timeline bounds")
)

