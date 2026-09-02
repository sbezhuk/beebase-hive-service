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
		INSERT INTO hives (id, apiary_id, user_id, name, notes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	_, err := r.db.Exec(ctx, q, h.ID, h.ApiaryID, h.UserID, h.Name, h.Notes, h.CreatedAt, h.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create hive: %w", err)
	}

	return nil
}

func (r *HiveRepository) GetByID(ctx context.Context, userID, hiveID uuid.UUID) (*hive.Hive, error) {
	const q = `
		SELECT id, apiary_id, user_id, name, notes, created_at, updated_at, deleted_at
		FROM hives
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	var h hive.Hive

	err := r.db.QueryRow(ctx, q, hiveID, userID).Scan(
		&h.ID, &h.ApiaryID, &h.UserID, &h.Name, &h.Notes, &h.CreatedAt, &h.UpdatedAt, &h.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, hive.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get hive: %w", err)
	}

	return &h, nil
}

func (r *HiveRepository) ListByUser(ctx context.Context, userID uuid.UUID, p pagination.Params) ([]*hive.Hive, int, error) {
	const countQ = `
		SELECT count(*)
		FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
	`

	var total int
	if err := r.db.QueryRow(ctx, countQ, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count hives: %w", err)
	}

	const q = `
		SELECT id, apiary_id, user_id, name, notes, created_at, updated_at, deleted_at
		FROM hives
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC, id ASC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Query(ctx, q, userID, p.Limit, p.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list hives: %w", err)
	}
	defer rows.Close()

	hives := []*hive.Hive{}
	for rows.Next() {
		var h hive.Hive
		if err := rows.Scan(&h.ID, &h.ApiaryID, &h.UserID, &h.Name, &h.Notes, &h.CreatedAt, &h.UpdatedAt, &h.DeletedAt); err != nil {
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
		SET name = $1, notes = $2, updated_at = $3
		WHERE id = $4 AND user_id = $5 AND deleted_at IS NULL
	`

	tag, err := r.db.Exec(ctx, q, h.Name, h.Notes, h.UpdatedAt, h.ID, h.UserID)
	if err != nil {
		return fmt.Errorf("postgres: update hive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return hive.ErrNotFound
	}

	return nil
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
