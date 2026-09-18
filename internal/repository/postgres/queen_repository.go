package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sbezhuk/beebase-common/pagination"

	"github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// Transactor allows starting database transactions when atomic multi-step operations are required.
type Transactor interface {
	Querier
	Begin(ctx context.Context) (pgx.Tx, error)
}

// QueenRepository implements domain/queen.Repository against PostgreSQL.
type QueenRepository struct {
	db   Querier
	pool Transactor
}

// NewQueenRepository constructs a QueenRepository.
func NewQueenRepository(pool Transactor) *QueenRepository {
	return &QueenRepository{
		db:   pool,
		pool: pool,
	}
}

func (r *QueenRepository) InsertInChain(ctx context.Context, q *queen.Queen, predecessorReason *queen.ReplacementReason) error {
	if r.pool == nil {
		return fmt.Errorf("postgres: transactor pool not configured")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx for insert in chain: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the hive's queen chain using a transaction-level advisory lock
	const lockSql = `SELECT pg_advisory_xact_lock(hashtext('hive_queens:' || $1::text))`
	if _, err := tx.Exec(ctx, lockSql, q.HiveID); err != nil {
		return fmt.Errorf("postgres: lock queen chain: %w", err)
	}

	// Fetch existing queens for this hive ordered by introduced_at ASC
	const fetchSql = `
		SELECT id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
		FROM hive_queens
		WHERE hive_id = $1
		ORDER BY introduced_at ASC
	`
	rows, err := tx.Query(ctx, fetchSql, q.HiveID)
	if err != nil {
		return fmt.Errorf("postgres: query queens in chain: %w", err)
	}
	defer rows.Close()

	var existing []*queen.Queen
	for rows.Next() {
		var item queen.Queen
		if err := rows.Scan(
			&item.ID,
			&item.HiveID,
			&item.MarkedAt,
			&item.IntroducedAt,
			&item.RemovedAt,
			&item.ReplacementReason,
			&item.Notes,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return fmt.Errorf("postgres: scan queen in chain: %w", err)
		}
		existing = append(existing, &item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: iterate queen chain rows: %w", err)
	}

	// Invariant: strictly increasing introduced_at (no duplicates)
	for _, ex := range existing {
		if ex.IntroducedAt.Equal(q.IntroducedAt) {
			return queen.ErrDuplicateIntroducedAt
		}
	}

	n := len(existing)
	if n == 0 {
		// First queen in hive: cannot have a predecessor, so replacement reason is not allowed
		if predecessorReason != nil {
			return queen.ErrReplacementReasonNotAllowed
		}
		// First queen in hive: becomes current
		q.RemovedAt = nil
		q.ReplacementReason = nil
	} else if q.IntroducedAt.After(existing[n-1].IntroducedAt) {
		// Appending newer queen: previous latest queen becomes historical ending at q.IntroducedAt
		prev := existing[n-1]
		const updatePrevSql = `
			UPDATE hive_queens
			SET removed_at = $1, replacement_reason = $2, updated_at = now()
			WHERE id = $3
		`
		if _, err := tx.Exec(ctx, updatePrevSql, q.IntroducedAt, predecessorReason, prev.ID); err != nil {
			return fmt.Errorf("postgres: update previous queen removed_at and reason: %w", err)
		}
		q.RemovedAt = nil
		// The new queen owns no replacement_reason of its own yet: predecessorReason
		// describes why prev was replaced, and belongs on prev's own row.
		q.ReplacementReason = nil
	} else if q.IntroducedAt.Before(existing[0].IntroducedAt) {
		// Inserting before the oldest queen: no predecessor exists, so replacement reason is not allowed
		if predecessorReason != nil {
			return queen.ErrReplacementReasonNotAllowed
		}
		// Inserting before the oldest queen: new queen ends where the old oldest began
		successorIntro := existing[0].IntroducedAt
		q.RemovedAt = &successorIntro
		q.ReplacementReason = nil
	} else {
		// Inserting between two existing queens: find predecessor and successor
		var prev, next *queen.Queen
		for i := 0; i < n-1; i++ {
			if existing[i].IntroducedAt.Before(q.IntroducedAt) && existing[i+1].IntroducedAt.After(q.IntroducedAt) {
				prev = existing[i]
				next = existing[i+1]
				break
			}
		}
		if prev == nil || next == nil {
			return fmt.Errorf("postgres: failed to find neighbor interval for insertion")
		}

		// Update predecessor's removed_at to q.IntroducedAt and replacement_reason to predecessorReason
		const updatePrevSql = `
			UPDATE hive_queens
			SET removed_at = $1, replacement_reason = $2, updated_at = now()
			WHERE id = $3
		`
		if _, err := tx.Exec(ctx, updatePrevSql, q.IntroducedAt, predecessorReason, prev.ID); err != nil {
			return fmt.Errorf("postgres: update predecessor removed_at and reason: %w", err)
		}
		successorIntro := next.IntroducedAt
		q.RemovedAt = &successorIntro
		// The newly inserted queen owns no replacement_reason of its own yet:
		// predecessorReason describes why prev was replaced, and belongs on prev's own row.
		q.ReplacementReason = nil
	}

	const insertSql = `
		INSERT INTO hive_queens (id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	// The newly inserted queen's row always stores replacement_reason = NULL:
	// it hasn't been replaced yet, so it owns no replacement reason of its own.
	_, err = tx.Exec(ctx, insertSql,
		q.ID,
		q.HiveID,
		q.MarkedAt,
		q.IntroducedAt,
		q.RemovedAt,
		nil,
		q.Notes,
		q.CreatedAt,
		q.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			if pgErr.ConstraintName == "idx_hive_queens_hive_introduced" {
				return queen.ErrDuplicateIntroducedAt
			}
			return queen.ErrActiveQueenExists
		}
		return fmt.Errorf("postgres: insert queen in chain: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit insert in chain: %w", err)
	}

	return nil
}

func (r *QueenRepository) UpdateInChain(ctx context.Context, hiveID, queenID uuid.UUID, markedAt time.Time, introducedAt time.Time, replacementReason *queen.ReplacementReason, hasReplacementReason bool, notes string) (*queen.Queen, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("postgres: transactor pool not configured")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx for update in chain: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockSql = `SELECT pg_advisory_xact_lock(hashtext('hive_queens:' || $1::text))`
	if _, err := tx.Exec(ctx, lockSql, hiveID); err != nil {
		return nil, fmt.Errorf("postgres: lock queen chain for update: %w", err)
	}

	const fetchSql = `
		SELECT id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
		FROM hive_queens
		WHERE hive_id = $1
		ORDER BY introduced_at ASC
	`
	rows, err := tx.Query(ctx, fetchSql, hiveID)
	if err != nil {
		return nil, fmt.Errorf("postgres: query queens for update: %w", err)
	}
	defer rows.Close()

	var existing []*queen.Queen
	targetIdx := -1
	for rows.Next() {
		var item queen.Queen
		if err := rows.Scan(
			&item.ID,
			&item.HiveID,
			&item.MarkedAt,
			&item.IntroducedAt,
			&item.RemovedAt,
			&item.ReplacementReason,
			&item.Notes,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan queen in update: %w", err)
		}
		if item.ID == queenID {
			targetIdx = len(existing)
		}
		existing = append(existing, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate rows in update: %w", err)
	}

	if targetIdx == -1 {
		return nil, queen.ErrNotFound
	}

	// If oldest queen in chain, it has no predecessor / incoming transition.
	// Supplying a non-null replacementReason is not allowed.
	if targetIdx == 0 && hasReplacementReason && replacementReason != nil {
		return nil, queen.ErrReplacementReasonNotAllowed
	}

	// Neighbor bound check:
	// If predecessor exists: introducedAt must be strictly greater than predecessor.IntroducedAt
	if targetIdx > 0 {
		prev := existing[targetIdx-1]
		if !introducedAt.After(prev.IntroducedAt) {
			return nil, queen.ErrTimelineInvalid
		}
	}
	// If successor exists: introducedAt must be strictly less than successor.IntroducedAt
	if targetIdx < len(existing)-1 {
		next := existing[targetIdx+1]
		if !introducedAt.Before(next.IntroducedAt) {
			return nil, queen.ErrTimelineInvalid
		}
	}

	// If predecessor exists, update its removed_at (if introducedAt changed) and/or its own
	// replacement_reason (if hasReplacementReason) - that column describes why the predecessor
	// was replaced, so editing "the reason for this transition" always writes the predecessor's row.
	if targetIdx > 0 {
		prev := existing[targetIdx-1]
		needsPrevUpdate := false
		newPrevRemovedAt := prev.RemovedAt
		if !existing[targetIdx].IntroducedAt.Equal(introducedAt) {
			newPrevRemovedAt = &introducedAt
			needsPrevUpdate = true
		}
		newPrevReason := prev.ReplacementReason
		if hasReplacementReason {
			newPrevReason = replacementReason
			needsPrevUpdate = true
		}
		if needsPrevUpdate {
			const updatePrevSql = `
				UPDATE hive_queens
				SET removed_at = $1, replacement_reason = $2, updated_at = now()
				WHERE id = $3
			`
			if _, err := tx.Exec(ctx, updatePrevSql, newPrevRemovedAt, newPrevReason, prev.ID); err != nil {
				return nil, fmt.Errorf("postgres: update predecessor on edit: %w", err)
			}
		}
	}

	// Update the target queen's own record. Its own physical replacement_reason column is NOT
	// changed here - it describes why the TARGET itself was replaced (set only when its own
	// successor is created/edited) - but is still returned as-is via RETURNING.
	const updateSql = `
		UPDATE hive_queens
		SET marked_at = $1, introduced_at = $2, notes = $3, updated_at = now()
		WHERE id = $4 AND hive_id = $5
		RETURNING id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
	`
	var updated queen.Queen
	err = tx.QueryRow(ctx, updateSql,
		markedAt,
		introducedAt,
		notes,
		queenID,
		hiveID,
	).Scan(
		&updated.ID,
		&updated.HiveID,
		&updated.MarkedAt,
		&updated.IntroducedAt,
		&updated.RemovedAt,
		&updated.ReplacementReason,
		&updated.Notes,
		&updated.CreatedAt,
		&updated.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return nil, queen.ErrDuplicateIntroducedAt
		}
		return nil, fmt.Errorf("postgres: update queen in chain: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: commit update in chain: %w", err)
	}

	return &updated, nil
}

func (r *QueenRepository) DeleteLatest(ctx context.Context, hiveID, queenID uuid.UUID) error {
	if r.pool == nil {
		return fmt.Errorf("postgres: transactor pool not configured")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx for delete latest: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockSql = `SELECT pg_advisory_xact_lock(hashtext('hive_queens:' || $1::text))`
	if _, err := tx.Exec(ctx, lockSql, hiveID); err != nil {
		return fmt.Errorf("postgres: lock queen chain for delete: %w", err)
	}

	// Fetch all queens ordered newest first
	const fetchSql = `
		SELECT id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
		FROM hive_queens
		WHERE hive_id = $1
		ORDER BY introduced_at DESC
	`
	rows, err := tx.Query(ctx, fetchSql, hiveID)
	if err != nil {
		return fmt.Errorf("postgres: query queens for delete: %w", err)
	}
	defer rows.Close()

	var existing []*queen.Queen
	found := false
	for rows.Next() {
		var item queen.Queen
		if err := rows.Scan(
			&item.ID,
			&item.HiveID,
			&item.MarkedAt,
			&item.IntroducedAt,
			&item.RemovedAt,
			&item.ReplacementReason,
			&item.Notes,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return fmt.Errorf("postgres: scan queen in delete: %w", err)
		}
		if item.ID == queenID {
			found = true
		}
		existing = append(existing, &item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: iterate rows in delete: %w", err)
	}

	if !found {
		return queen.ErrNotFound
	}

	// Only the latest queen may be deleted
	if existing[0].ID != queenID {
		return queen.ErrQueenNotLatest
	}

	// Delete the latest queen
	const deleteSql = `DELETE FROM hive_queens WHERE id = $1 AND hive_id = $2`
	if _, err := tx.Exec(ctx, deleteSql, queenID, hiveID); err != nil {
		return fmt.Errorf("postgres: execute delete queen: %w", err)
	}

	// If a predecessor exists, roll it back to current (removed_at = NULL, replacement_reason = NULL)
	if len(existing) > 1 {
		prev := existing[1]
		const updatePrevSql = `
			UPDATE hive_queens
			SET removed_at = NULL, replacement_reason = NULL, updated_at = now()
			WHERE id = $1
		`
		if _, err := tx.Exec(ctx, updatePrevSql, prev.ID); err != nil {
			return fmt.Errorf("postgres: rollback predecessor to current: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit delete latest tx: %w", err)
	}

	return nil
}

func (r *QueenRepository) GetCurrentByHiveID(ctx context.Context, hiveID uuid.UUID) (*queen.Queen, error) {
	// replacement_reason is each row's own physical column: it describes why
	// THIS queen was replaced, not why its predecessor was replaced. The
	// current queen always has removed_at IS NULL and, per the active-has-no-
	// reason constraint, replacement_reason IS NULL too.
	const sql = `
		SELECT id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
		FROM hive_queens
		WHERE hive_id = $1 AND removed_at IS NULL
		LIMIT 1
	`
	var q queen.Queen
	err := r.db.QueryRow(ctx, sql, hiveID).Scan(
		&q.ID,
		&q.HiveID,
		&q.MarkedAt,
		&q.IntroducedAt,
		&q.RemovedAt,
		&q.ReplacementReason,
		&q.Notes,
		&q.CreatedAt,
		&q.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, queen.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get current queen: %w", err)
	}
	return &q, nil
}

func (r *QueenRepository) GetByID(ctx context.Context, hiveID, queenID uuid.UUID) (*queen.Queen, error) {
	// replacement_reason is this row's own physical column: why THIS queen was
	// replaced (NULL if it's current or hasn't been replaced yet).
	const sql = `
		SELECT id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
		FROM hive_queens
		WHERE hive_id = $1 AND id = $2
	`
	var q queen.Queen
	err := r.db.QueryRow(ctx, sql, hiveID, queenID).Scan(
		&q.ID,
		&q.HiveID,
		&q.MarkedAt,
		&q.IntroducedAt,
		&q.RemovedAt,
		&q.ReplacementReason,
		&q.Notes,
		&q.CreatedAt,
		&q.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, queen.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get queen by id: %w", err)
	}
	return &q, nil
}

func (r *QueenRepository) ListHistoryByHiveID(ctx context.Context, hiveID uuid.UUID) ([]*queen.Queen, error) {
	// replacement_reason is each row's own physical column: why THAT queen was
	// replaced. The replaced (older) queen in a transition owns the reason;
	// the queen created in that transition owns NULL until it, in turn, is replaced.
	const sql = `
		SELECT id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
		FROM hive_queens
		WHERE hive_id = $1
		ORDER BY introduced_at DESC, created_at DESC, id DESC
	`
	rows, err := r.db.Query(ctx, sql, hiveID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list queen history: %w", err)
	}
	defer rows.Close()

	var queens []*queen.Queen
	for rows.Next() {
		var q queen.Queen
		if err := rows.Scan(
			&q.ID,
			&q.HiveID,
			&q.MarkedAt,
			&q.IntroducedAt,
			&q.RemovedAt,
			&q.ReplacementReason,
			&q.Notes,
			&q.CreatedAt,
			&q.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan queen: %w", err)
		}
		queens = append(queens, &q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list queen history rows: %w", err)
	}
	if queens == nil {
		queens = []*queen.Queen{}
	}
	return queens, nil
}

func (r *QueenRepository) ListHistoryPageByHiveID(ctx context.Context, hiveID uuid.UUID, p pagination.Params) ([]*queen.Queen, int, error) {
	const sql = `
		SELECT id, hive_id, marked_at, introduced_at, removed_at, replacement_reason, notes, created_at, updated_at
		FROM hive_queens
		WHERE hive_id = $1
		ORDER BY introduced_at DESC, created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.Query(ctx, sql, hiveID, p.Limit, p.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list queen history page: %w", err)
	}
	defer rows.Close()
	var queens []*queen.Queen
	for rows.Next() {
		var q queen.Queen
		if err := rows.Scan(&q.ID, &q.HiveID, &q.MarkedAt, &q.IntroducedAt, &q.RemovedAt, &q.ReplacementReason, &q.Notes, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("postgres: scan queen page: %w", err)
		}
		queens = append(queens, &q)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: list queen history page rows: %w", err)
	}
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM hive_queens WHERE hive_id = $1`, hiveID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count queen history: %w", err)
	}
	if queens == nil {
		queens = []*queen.Queen{}
	}
	return queens, total, nil
}
