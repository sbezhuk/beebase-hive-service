package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sbezhuk/beebase-common/pagination"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// minSearchLength is the minimum number of characters required for the
// search term to be applied. Shorter terms produce noisy results and put
// unnecessary load on the database.
const minSearchLength = 3

// uniqueViolationCode is PostgreSQL's SQLSTATE for a unique constraint
// violation.
const uniqueViolationCode = "23505"

// createdAtOrderClause returns the ORDER BY clause for a list query. When
// sortOrder is nil, defaultClause (the query's normal, pre-existing order)
// is used unchanged; otherwise the list is ordered by creation date in the
// requested direction, with id tied to the same direction as a stable
// tiebreaker (matching the convention every other ORDER BY in this
// repository already follows).
func createdAtOrderClause(sortOrder *string, defaultClause string) string {
	if sortOrder == nil {
		return defaultClause
	}
	dir := "ASC"
	if *sortOrder == "desc" {
		dir = "DESC"
	}
	return fmt.Sprintf("created_at %s, id %s", dir, dir)
}

// HiveRepository implements domain/hive.Repository against PostgreSQL.
// Every method scopes its query by user_id, so a user can never read or
// write a hive they don't own: there's no separate ownership-check step
// to forget.
type HiveRepository struct {
	db Querier
}

// NewHiveRepository returns a HiveRepository backed by db.
func NewHiveRepository(db Querier) *HiveRepository {
	return &HiveRepository{db: db}
}

func (r *HiveRepository) Create(ctx context.Context, h *hive.Hive) error {
	const q = `
		INSERT INTO hives (id, apiary_id, user_id, name, notes, images, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.db.Exec(ctx, q, h.ID, h.ApiaryID, h.UserID, h.Name, h.Notes, images(h.Images), h.CreatedAt, h.UpdatedAt)
	if err != nil {
		if isUniqueNameViolation(err) {
			return hive.ErrNameTaken
		}
		return fmt.Errorf("postgres: create hive: %w", err)
	}

	return nil
}

// CountByUser returns the total number of non-deleted hives owned by userID across all apiaries.
func (r *HiveRepository) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	const q = `
		SELECT count(*)
		FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	var count int
	if err := r.db.QueryRow(ctx, q, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres: count hives: %w", err)
	}
	return count, nil
}

// CreateWithLimit creates a new hive, but only if the user currently owns
// fewer than maxCount active hives across all apiaries. If maxCount <= 0,
// creation is unlimited. The count (and the advisory lock guarding it) is
// scoped to h.UserID, not h.ApiaryID: the product's "5 writable hives"
// entitlement is a per-user, account-wide quota (see application/hive.
// FreeMaxHives), so two concurrent creates into different apiaries for the
// same user must still be serialized against the same shared limit.
// Parent-apiary writability is a separate gate enforced by the
// application layer before this is ever called (see application/hive.
// Service.Create).
func (r *HiveRepository) CreateWithLimit(ctx context.Context, h *hive.Hive, maxCount int) error {
	if maxCount <= 0 {
		return r.Create(ctx, h)
	}

	pool, isPool := r.db.(*pgxpool.Pool)
	if isPool {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("postgres: begin tx: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		const lockQ = `SELECT pg_advisory_xact_lock(hashtext('hive_limit:' || $1::text))`
		if _, err := tx.Exec(ctx, lockQ, h.UserID); err != nil {
			return fmt.Errorf("postgres: acquire advisory lock: %w", err)
		}

		const countQ = `SELECT count(*) FROM hives WHERE user_id = $1 AND deleted_at IS NULL`
		var count int
		if err := tx.QueryRow(ctx, countQ, h.UserID).Scan(&count); err != nil {
			return fmt.Errorf("postgres: count hives: %w", err)
		}
		if count >= maxCount {
			return hive.ErrLimitReached
		}

		const insertQ = `
			INSERT INTO hives (id, apiary_id, user_id, name, notes, images, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`
		if _, err := tx.Exec(ctx, insertQ, h.ID, h.ApiaryID, h.UserID, h.Name, h.Notes, images(h.Images), h.CreatedAt, h.UpdatedAt); err != nil {
			if isUniqueNameViolation(err) {
				return hive.ErrNameTaken
			}
			return fmt.Errorf("postgres: create hive: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit tx: %w", err)
		}
		return nil
	}

	const lockQ = `SELECT pg_advisory_xact_lock(hashtext('hive_limit:' || $1::text))`
	if _, err := r.db.Exec(ctx, lockQ, h.UserID); err != nil {
		return fmt.Errorf("postgres: acquire advisory lock: %w", err)
	}

	const countQ = `SELECT count(*) FROM hives WHERE user_id = $1 AND deleted_at IS NULL`
	var count int
	if err := r.db.QueryRow(ctx, countQ, h.UserID).Scan(&count); err != nil {
		return fmt.Errorf("postgres: count hives: %w", err)
	}
	if count >= maxCount {
		return hive.ErrLimitReached
	}

	return r.Create(ctx, h)
}

// WritableIDs returns the ids of the oldest up to limit non-deleted hives
// owned by userID across all their apiaries, ordered created_at ASC, id
// ASC. A limit <= 0 returns every hive id userID owns.
func (r *HiveRepository) WritableIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error) {
	q := `
		SELECT id FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC, id ASC
	`
	args := []any{userID}
	if limit > 0 {
		q += " LIMIT $2"
		args = append(args, limit)
	}

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: writable hive ids: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scan writable hive id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: writable hive ids: %w", err)
	}

	return ids, nil
}

func (r *HiveRepository) GetByID(ctx context.Context, userID, hiveID uuid.UUID) (*hive.Hive, error) {
	const q = `
		SELECT id, apiary_id, user_id, name, notes, images, created_at, updated_at, deleted_at
		FROM hives
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	var h hive.Hive

	err := r.db.QueryRow(ctx, q, hiveID, userID).Scan(
		&h.ID, &h.ApiaryID, &h.UserID, &h.Name, &h.Notes, &h.Images, &h.CreatedAt, &h.UpdatedAt, &h.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, hive.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get hive: %w", err)
	}

	return &h, nil
}

func (r *HiveRepository) ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspectionOnly bool, needsInspectionHiveIDs []uuid.UUID) ([]*hive.Hive, int, error) {
	return r.list(ctx, userID, nil, p, search, sortOrder, needsInspectionOnly, needsInspectionHiveIDs)
}

func (r *HiveRepository) ListByApiary(ctx context.Context, userID, apiaryID uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspectionOnly bool, needsInspectionHiveIDs []uuid.UUID) ([]*hive.Hive, int, error) {
	return r.list(ctx, userID, &apiaryID, p, search, sortOrder, needsInspectionOnly, needsInspectionHiveIDs)
}

func (r *HiveRepository) list(ctx context.Context, userID uuid.UUID, apiaryID *uuid.UUID, p pagination.Params, search, sortOrder *string, needsInspectionOnly bool, needsInspectionHiveIDs []uuid.UUID) ([]*hive.Hive, int, error) {
	countQ := `
		SELECT count(*)
		FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	q := `
		SELECT id, apiary_id, user_id, name, notes, images, created_at, updated_at, deleted_at
		FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	countArgs := []any{userID}
	argIdx := 2

	if apiaryID != nil {
		cond := fmt.Sprintf(" AND apiary_id = $%d", argIdx)
		countQ += cond
		q += cond
		countArgs = append(countArgs, *apiaryID)
		argIdx++
	}

	if needsInspectionOnly {
		// needsInspectionHiveIDs is already the full "needs inspection"
		// set the application layer computed - possibly empty, which
		// correctly matches zero rows here (= ANY of an empty array is
		// false for every row), not "no filter".
		cond := fmt.Sprintf(" AND id = ANY($%d)", argIdx)
		countQ += cond
		q += cond
		ids := needsInspectionHiveIDs
		if ids == nil {
			ids = []uuid.UUID{}
		}
		countArgs = append(countArgs, ids)
		argIdx++
	}

	listArgs := make([]any, len(countArgs))
	copy(listArgs, countArgs)

	if search != nil && len(*search) >= minSearchLength {
		pattern := "%" + *search + "%"
		cond := fmt.Sprintf(" AND (name ILIKE $%d OR notes ILIKE $%d)", argIdx, argIdx)
		countQ += cond
		q += cond
		countArgs = append(countArgs, pattern)
		listArgs = append(listArgs, pattern)
		argIdx++
	}

	q += fmt.Sprintf(`
		ORDER BY %s
		LIMIT $%d OFFSET $%d`, createdAtOrderClause(sortOrder, "created_at ASC, id ASC"), argIdx, argIdx+1)
	listArgs = append(listArgs, p.Limit, p.Offset())

	var total int
	if err := r.db.QueryRow(ctx, countQ, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count hives: %w", err)
	}

	rows, err := r.db.Query(ctx, q, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list hives: %w", err)
	}
	defer rows.Close()

	hives := []*hive.Hive{}
	for rows.Next() {
		var h hive.Hive
		if err := rows.Scan(&h.ID, &h.ApiaryID, &h.UserID, &h.Name, &h.Notes, &h.Images, &h.CreatedAt, &h.UpdatedAt, &h.DeletedAt); err != nil {
			return nil, 0, fmt.Errorf("postgres: scan hive: %w", err)
		}
		hives = append(hives, &h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: list hives: %w", err)
	}

	return hives, total, nil
}

func (r *HiveRepository) Update(ctx context.Context, h *hive.Hive) error {
	const q = `
		UPDATE hives
		SET name = $1, notes = $2, images = $3, updated_at = $4
		WHERE id = $5 AND user_id = $6 AND deleted_at IS NULL
	`

	tag, err := r.db.Exec(ctx, q, h.Name, h.Notes, images(h.Images), h.UpdatedAt, h.ID, h.UserID)
	if err != nil {
		if isUniqueNameViolation(err) {
			return hive.ErrNameTaken
		}
		return fmt.Errorf("postgres: update hive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return hive.ErrNotFound
	}

	return nil
}

func isUniqueNameViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == uniqueViolationCode &&
		pgErr.ConstraintName == "idx_hives_apiary_id_name_unique_active"
}

// images coalesces a nil slice to an empty one - the images column is
// NOT NULL, and pgx would otherwise encode a nil Go slice as SQL NULL.
func images(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

func (r *HiveRepository) ListAllByApiary(ctx context.Context, userID, apiaryID uuid.UUID) ([]*hive.Hive, error) {
	const q = `
		SELECT id, apiary_id, user_id, name, notes, images, created_at, updated_at, deleted_at
		FROM hives
		WHERE apiary_id = $1 AND user_id = $2
	`

	rows, err := r.db.Query(ctx, q, apiaryID, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list hives by apiary: %w", err)
	}
	defer rows.Close()

	hives := []*hive.Hive{}
	for rows.Next() {
		var h hive.Hive
		if err := rows.Scan(&h.ID, &h.ApiaryID, &h.UserID, &h.Name, &h.Notes, &h.Images, &h.CreatedAt, &h.UpdatedAt, &h.DeletedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan hive: %w", err)
		}
		hives = append(hives, &h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list hives by apiary: %w", err)
	}

	return hives, nil
}

func (r *HiveRepository) ListIDsByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT id FROM hives WHERE user_id = $1 AND deleted_at IS NULL`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list hive ids by user: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scan hive id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list hive ids by user: %w", err)
	}

	return ids, nil
}

func (r *HiveRepository) DistinctApiaryIDsWithHives(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT DISTINCT apiary_id FROM hives WHERE user_id = $1 AND deleted_at IS NULL`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: distinct apiary ids with hives: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scan apiary id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: distinct apiary ids with hives: %w", err)
	}

	return ids, nil
}

func (r *HiveRepository) HardDelete(ctx context.Context, userID, hiveID uuid.UUID) error {
	const q = `DELETE FROM hives WHERE id = $1 AND user_id = $2`

	tag, err := r.db.Exec(ctx, q, hiveID, userID)
	if err != nil {
		return fmt.Errorf("postgres: hard delete hive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return hive.ErrNotFound
	}

	return nil
}
