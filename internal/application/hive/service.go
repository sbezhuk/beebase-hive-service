// Package hive implements the hive use cases: create, get, list, update,
// and delete. It depends only on the domain/hive port and the
// ApiaryVerifier port declared in this package, never on HTTP or
// PostgreSQL directly.
package hive

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/pagination"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// Service implements the hive use cases. Every method takes the
// requesting user's ID (extracted from their verified access token by the
// transport layer) and passes it straight through to the repository,
// which enforces ownership at the query level.
type Service struct {
	hives       hive.Repository
	apiaries    ApiaryVerifier
	inspections InspectionDeleter
	media       MediaDeleter
}

// NewService constructs a Service.
func NewService(hives hive.Repository, apiaries ApiaryVerifier, inspections InspectionDeleter, media MediaDeleter) *Service {
	return &Service{hives: hives, apiaries: apiaries, inspections: inspections, media: media}
}

// Create creates a new hive owned by userID under in.ApiaryID, after
// confirming with apiary-service that userID actually owns that apiary.
// accessToken is the caller's own access token, forwarded to
// apiary-service so it can run its own, identical ownership check rather
// than this service trusting a client-supplied user/apiary pairing.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, accessToken string, in CreateInput) (*hive.Hive, error) {
	if err := s.apiaries.Verify(ctx, accessToken, in.ApiaryID); err != nil {
		return nil, err
	}

	h := hive.New(userID, in.ApiaryID, in.Name, in.Location, in.Notes)
	if err := s.hives.Create(ctx, h); err != nil {
		return nil, fmt.Errorf("hive: create: %w", err)
	}

	return h, nil
}

// Get returns the hive identified by hiveID, if it belongs to userID.
func (s *Service) Get(ctx context.Context, userID, hiveID uuid.UUID) (*hive.Hive, error) {
	return s.hives.GetByID(ctx, userID, hiveID)
}

// List returns the page of hives described by p, out of every hive
// belonging to userID.
func (s *Service) List(ctx context.Context, userID uuid.UUID, p pagination.Params) ([]*hive.Hive, int, error) {
	return s.hives.ListByUser(ctx, userID, p)
}

// Update replaces the editable fields of the hive identified by hiveID,
// if it belongs to userID.
func (s *Service) Update(ctx context.Context, userID, hiveID uuid.UUID, in UpdateInput) (*hive.Hive, error) {
	h, err := s.hives.GetByID(ctx, userID, hiveID)
	if err != nil {
		return nil, err
	}

	h.Name = in.Name
	h.Location = in.Location
	h.Notes = in.Notes
	h.UpdatedAt = time.Now().UTC()

	if err := s.hives.Update(ctx, h); err != nil {
		return nil, fmt.Errorf("hive: update: %w", err)
	}

	return h, nil
}

// Delete cascades: every inspection and every media item belonging to
// hiveID is deleted first (leaf-most first), then the hive itself is
// hard-deleted. accessToken is the caller's own access token, forwarded
// to inspection-service and media-service so each can run its own
// ownership check. If any step fails, Delete stops and returns the error
// without rolling back steps that already succeeded - there is no
// distributed transaction across these services, by design.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID, accessToken string, hiveID uuid.UUID) error {
	if _, err := s.hives.GetByID(ctx, userID, hiveID); err != nil {
		return err
	}
	return s.deleteCascade(ctx, userID, accessToken, hiveID)
}

// DeleteByApiary cascades every hive under apiaryID, in-process (no
// self-HTTP-call): for each hive it runs the identical cascade Delete
// uses. It stops at the first hive that fails, leaving hives already
// fully deleted earlier in the loop deleted - the same no-rollback
// contract as Delete, just applied across a batch.
func (s *Service) DeleteByApiary(ctx context.Context, userID uuid.UUID, accessToken string, apiaryID uuid.UUID) error {
	hives, err := s.hives.ListByApiary(ctx, userID, apiaryID)
	if err != nil {
		return fmt.Errorf("hive: list by apiary: %w", err)
	}

	for _, h := range hives {
		if err := s.deleteCascade(ctx, userID, accessToken, h.ID); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) deleteCascade(ctx context.Context, userID uuid.UUID, accessToken string, hiveID uuid.UUID) error {
	if err := s.inspections.DeleteByHive(ctx, accessToken, hiveID); err != nil {
		return err
	}
	if err := s.media.DeleteByOwner(ctx, accessToken, hiveID); err != nil {
		return err
	}
	return s.hives.HardDelete(ctx, userID, hiveID)
}
