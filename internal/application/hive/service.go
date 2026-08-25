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

	"github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
)

// Service implements the hive use cases. Every method takes the
// requesting user's ID (extracted from their verified access token by the
// transport layer) and passes it straight through to the repository,
// which enforces ownership at the query level.
type Service struct {
	hives    hive.Repository
	apiaries ApiaryVerifier
}

// NewService constructs a Service.
func NewService(hives hive.Repository, apiaries ApiaryVerifier) *Service {
	return &Service{hives: hives, apiaries: apiaries}
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

// List returns every hive belonging to userID.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]*hive.Hive, error) {
	return s.hives.ListByUser(ctx, userID)
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

// Delete deletes the hive identified by hiveID, if it belongs to userID.
func (s *Service) Delete(ctx context.Context, userID, hiveID uuid.UUID) error {
	return s.hives.Delete(ctx, userID, hiveID)
}
