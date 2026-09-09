package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sbezhuk/beebase-common/pagination"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// minSearchLength is the minimum number of characters required for the
// search term to be applied. Shorter terms produce noisy results and put
// unnecessary load on the database.
const minSearchLength = 3

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
		return fmt.Errorf("postgres: create hive: %w", err)
	}

	return nil
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

func (r *HiveRepository) ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params, search *string) ([]*hive.Hive, int, error) {
	countQ := `
		SELECT count(*)
		FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	countArgs := []any{userID}

	q := `
		SELECT id, apiary_id, user_id, name, notes, images, created_at, updated_at, deleted_at
		FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	listArgs := []any{userID}

	if search != nil && len(*search) >= minSearchLength {
		pattern := "%" + *search + "%"
		countQ += ` AND (name ILIKE $2 OR notes ILIKE $2)`
		countArgs = append(countArgs, pattern)
		q += fmt.Sprintf(` AND (name ILIKE $2 OR notes ILIKE $2)`)
		q += fmt.Sprintf(`
		ORDER BY created_at ASC, id ASC
		LIMIT $3 OFFSET $4`)
		listArgs = append(listArgs, pattern, p.Limit, p.Offset())
	} else {
		q += `
		ORDER BY created_at ASC, id ASC
		LIMIT $2 OFFSET $3`
		listArgs = append(listArgs, p.Limit, p.Offset())
	}

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
		return fmt.Errorf("postgres: update hive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return hive.ErrNotFound
	}

	return nil
}

// images coalesces a nil slice to an empty one - the images column is
// NOT NULL, and pgx would otherwise encode a nil Go slice as SQL NULL.
func images(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

func (r *HiveRepository) ListByApiary(ctx context.Context, userID, apiaryID uuid.UUID) ([]*hive.Hive, error) {
	const q = `
		SELECT id, apiary_id, user_id, name, notes, created_at, updated_at, deleted_at
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
		if err := rows.Scan(&h.ID, &h.ApiaryID, &h.UserID, &h.Name, &h.Notes, &h.CreatedAt, &h.UpdatedAt, &h.DeletedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan hive: %w", err)
		}
		hives = append(hives, &h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list hives by apiary: %w", err)
	}

	return hives, nil
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
