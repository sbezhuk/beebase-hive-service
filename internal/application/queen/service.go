package queen

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/sbezhuk/beebase-common/pagination"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

// Service implements the queen use cases for a hive within the strict lifecycle chain model.
type Service struct {
	queens        QueenRepository
	hives         HiveRepository
	apiaries      ApiaryVerifier
	subscriptions EntitlementResolver
}

// NewService constructs a queen application Service.
func NewService(
	queens QueenRepository,
	hives HiveRepository,
	apiaries ApiaryVerifier,
	subscriptions EntitlementResolver,
) *Service {
	return &Service{
		queens:        queens,
		hives:         hives,
		apiaries:      apiaries,
		subscriptions: subscriptions,
	}
}

// Create registers a new queen into the hive's strict chronological chain.
// It derives lifecycle boundaries automatically and transactionally.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, accessToken string, hiveID uuid.UUID, in CreateInput) (*domainqueen.Queen, error) {
	if in.MarkedAt.IsZero() {
		return nil, ErrMarkedAtRequired
	}
	if in.IntroducedAt.IsZero() {
		return nil, ErrIntroducedAtRequired
	}
	if domainqueen.IsFutureCalendarDate(in.IntroducedAt) {
		return nil, ErrIntroducedAtInFuture
	}
	if in.ReplacementReason != nil && !in.ReplacementReason.IsValid() {
		return nil, ErrReplacementReasonInvalid
	}

	h, err := s.hives.GetByID(ctx, userID, hiveID)
	if err != nil {
		return nil, err
	}

	if err := s.requireWritable(ctx, userID, accessToken, h); err != nil {
		return nil, err
	}

	newQueen := domainqueen.New(hiveID, in.MarkedAt, in.IntroducedAt, nil, nil, in.Notes)
	if err := s.queens.InsertInChain(ctx, newQueen, in.ReplacementReason); err != nil {
		return nil, err
	}

	return newQueen, nil
}

// GetCurrent returns the currently active queen for hiveID.
func (s *Service) GetCurrent(ctx context.Context, userID uuid.UUID, hiveID uuid.UUID) (*domainqueen.Queen, error) {
	if _, err := s.hives.GetByID(ctx, userID, hiveID); err != nil {
		return nil, err
	}
	return s.queens.GetCurrentByHiveID(ctx, hiveID)
}

// GetByID returns a specific queen by ID under hiveID.
func (s *Service) GetByID(ctx context.Context, userID uuid.UUID, hiveID, queenID uuid.UUID) (*domainqueen.Queen, error) {
	if _, err := s.hives.GetByID(ctx, userID, hiveID); err != nil {
		return nil, err
	}
	return s.queens.GetByID(ctx, hiveID, queenID)
}

// ListHistory returns all queens associated with hiveID, ordered newest first.
func (s *Service) ListHistory(ctx context.Context, userID uuid.UUID, hiveID uuid.UUID) ([]*domainqueen.Queen, error) {
	if _, err := s.hives.GetByID(ctx, userID, hiveID); err != nil {
		return nil, err
	}
	return s.queens.ListHistoryByHiveID(ctx, hiveID)
}

// ListHistoryPage returns one deterministic page for the public collection
// endpoint while retaining the lifecycle service's unpaginated method.
func (s *Service) ListHistoryPage(ctx context.Context, userID uuid.UUID, hiveID uuid.UUID, p pagination.Params) ([]*domainqueen.Queen, int, error) {
	if _, err := s.hives.GetByID(ctx, userID, hiveID); err != nil {
		return nil, 0, err
	}
	if repo, ok := s.queens.(domainqueen.PaginatedRepository); ok {
		return repo.ListHistoryPageByHiveID(ctx, hiveID, p)
	}
	all, err := s.queens.ListHistoryByHiveID(ctx, hiveID)
	if err != nil {
		return nil, 0, err
	}
	start := p.Offset()
	if start > len(all) {
		start = len(all)
	}
	end := start + p.Limit
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], len(all), nil
}

// Update edits metadata and/or introducedAt for an existing queen.
// The queen must remain strictly within its chain bounds (predecessor.introduced_at < introducedAt < successor.introduced_at).
func (s *Service) Update(ctx context.Context, userID uuid.UUID, accessToken string, hiveID, queenID uuid.UUID, in UpdateInput) (*domainqueen.Queen, error) {
	if in.MarkedAt.IsZero() {
		return nil, ErrMarkedAtRequired
	}
	if in.IntroducedAt.IsZero() {
		return nil, ErrIntroducedAtRequired
	}
	if domainqueen.IsFutureCalendarDate(in.IntroducedAt) {
		return nil, ErrIntroducedAtInFuture
	}
	if in.HasReplacementReason && in.ReplacementReason != nil && !in.ReplacementReason.IsValid() {
		return nil, ErrReplacementReasonInvalid
	}

	h, err := s.hives.GetByID(ctx, userID, hiveID)
	if err != nil {
		return nil, err
	}

	if err := s.requireWritable(ctx, userID, accessToken, h); err != nil {
		return nil, err
	}

	return s.queens.UpdateInChain(ctx, hiveID, queenID, in.MarkedAt, in.IntroducedAt, in.ReplacementReason, in.HasReplacementReason, in.Notes)
}

// Delete permanently removes the latest queen in the chain and rolls back the predecessor to current.
// If queenID is not the latest queen, ErrQueenNotLatest is returned.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID, accessToken string, hiveID, queenID uuid.UUID) error {
	h, err := s.hives.GetByID(ctx, userID, hiveID)
	if err != nil {
		return err
	}

	if err := s.requireWritable(ctx, userID, accessToken, h); err != nil {
		return err
	}

	return s.queens.DeleteLatest(ctx, hiveID, queenID)
}

func (s *Service) requireWritable(ctx context.Context, userID uuid.UUID, accessToken string, h *domainhive.Hive) error {
	entitlement, err := s.subscriptions.GetEntitlement(ctx, accessToken)
	if err != nil {
		return fmt.Errorf("queen: resolve entitlement: %w", err)
	}
	if entitlement != apphive.EntitlementFree {
		return nil
	}

	apiaryWritable, err := s.apiaries.Verify(ctx, accessToken, h.ApiaryID)
	if err != nil {
		return err
	}
	if !apiaryWritable {
		return ErrParentReadOnly
	}

	ids, err := s.hives.WritableIDs(ctx, userID, apphive.FreeMaxHives)
	if err != nil {
		return fmt.Errorf("queen: writable hive ids: %w", err)
	}
	for _, id := range ids {
		if id == h.ID {
			return nil
		}
	}
	return ErrReadOnly
}
