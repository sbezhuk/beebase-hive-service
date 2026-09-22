package report

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

var (
	ErrProRequired                 = errors.New("hive report requires pro")
	ErrReportGenerationUnavailable = errors.New("report_generation_unavailable")
)

type HiveReader interface {
	Get(context.Context, uuid.UUID, string, uuid.UUID) (*apphive.WithAccess, error)
}

type QueenHistoryReader interface {
	ListHistory(context.Context, uuid.UUID, uuid.UUID) ([]*domainqueen.Queen, error)
}

type EntitlementReader interface {
	GetEntitlement(context.Context, string) (string, error)
}

type InspectionReader interface {
	GetReportData(context.Context, uuid.UUID, time.Time, time.Time) (InspectionReportResponse, error)
}

type HarvestReader interface {
	GetReportData(context.Context, uuid.UUID, time.Time, time.Time) (HarvestReportResponse, error)
}

type Service struct {
	hives             HiveReader
	queens            QueenHistoryReader
	entitlements      EntitlementReader
	inspections       InspectionReader
	harvests          HarvestReader
	dependencyTimeout time.Duration
	now               func() time.Time
}

func NewService(hives HiveReader, queens QueenHistoryReader, entitlements EntitlementReader, inspections InspectionReader, harvests HarvestReader) *Service {
	return &Service{
		hives: hives, queens: queens, entitlements: entitlements,
		inspections: inspections, harvests: harvests,
		dependencyTimeout: 5 * time.Second, now: time.Now,
	}
}

// Assemble authorizes and assembles one complete HiveReport. Downstream reads
// are independent and are deliberately collected in parallel.
func (s *Service) Assemble(ctx context.Context, userID uuid.UUID, accessToken string, hiveID uuid.UUID, period Period, locale string) (*HiveReport, error) {
	hive, err := s.hives.Get(ctx, userID, accessToken, hiveID)
	if err != nil {
		return nil, err
	}
	entitlement, err := s.entitlements.GetEntitlement(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve entitlement: %v", ErrReportGenerationUnavailable, err)
	}
	if entitlement != apphive.EntitlementPro {
		return nil, ErrProRequired
	}
	if err := validatePeriod(period); err != nil {
		return nil, err
	}
	if err := ValidateLocale(locale); err != nil {
		return nil, err
	}

	queens, err := s.queens.ListHistory(ctx, userID, hiveID)
	if err != nil {
		return nil, fmt.Errorf("%w: load queen history: %v", ErrReportGenerationUnavailable, err)
	}

	inspectionResponse, harvestResponse, err := s.collectDependencies(ctx, hiveID, period)
	if err != nil {
		return nil, err
	}

	return &HiveReport{
		Metadata: ReportMetadata{From: period.From, To: period.To, Locale: locale, GeneratedAt: s.now().UTC()},
		Hive: HiveData{
			ID: hive.ID, ApiaryID: hive.ApiaryID, Name: hive.Name, Notes: hive.Notes,
			CreatedAt: hive.CreatedAt, UpdatedAt: hive.UpdatedAt,
		},
		Queens:        mapQueens(queens, period),
		Inspections:   nonNilInspections(inspectionResponse.Inspections),
		Health:        inspectionResponse.ColonyHealth,
		HealthHistory: inspectionResponse.HealthHistory,
		Harvests:      nonNilHarvests(harvestResponse.Harvests),
		HarvestTotals: nonNilHarvestTotals(harvestResponse.Totals),
	}, nil
}

func (s *Service) collectDependencies(ctx context.Context, hiveID uuid.UUID, period Period) (InspectionReportResponse, HarvestReportResponse, error) {
	dependencyCtx, cancel := context.WithTimeout(ctx, s.dependencyTimeout)
	defer cancel()
	type result struct {
		inspection *InspectionReportResponse
		harvest    *HarvestReportResponse
		err        error
	}
	results := make(chan result, 2)
	go func() {
		value, err := s.inspections.GetReportData(dependencyCtx, hiveID, period.From, period.To)
		results <- result{inspection: &value, err: err}
	}()
	go func() {
		value, err := s.harvests.GetReportData(dependencyCtx, hiveID, period.From, period.To)
		results <- result{harvest: &value, err: err}
	}()

	var inspectionResponse InspectionReportResponse
	var harvestResponse HarvestReportResponse
	for range 2 {
		item := <-results
		if item.err != nil {
			cancel()
			return InspectionReportResponse{}, HarvestReportResponse{}, fmt.Errorf("%w: %v", ErrReportGenerationUnavailable, item.err)
		}
		if item.inspection != nil {
			inspectionResponse = *item.inspection
		} else {
			harvestResponse = *item.harvest
		}
	}
	return inspectionResponse, harvestResponse, nil
}

func validatePeriod(period Period) error {
	if period.From.IsZero() || period.To.IsZero() {
		return errors.New("report period is required")
	}
	if period.From.After(period.To) {
		return errors.New("from must not be after to")
	}
	if period.To.After(period.From.AddDate(0, MaxPeriodMonths, 0)) {
		return fmt.Errorf("report period must not exceed %d months", MaxPeriodMonths)
	}
	return nil
}

func mapQueens(queens []*domainqueen.Queen, period Period) []QueenData {
	out := make([]QueenData, 0, len(queens))
	for _, queen := range queens {
		introduced := calendarDate(queen.IntroducedAt)
		removed := calendarDatePtr(queen.RemovedAt)
		if introduced.After(period.To) || (removed != nil && removed.Before(period.From)) {
			continue
		}
		color := queen.MarkingColor()
		var reason *string
		if queen.ReplacementReason != nil {
			value := string(*queen.ReplacementReason)
			reason = &value
		}
		out = append(out, QueenData{
			ID: queen.ID, MarkedAt: queen.MarkedAt, IntroducedAt: queen.IntroducedAt, RemovedAt: queen.RemovedAt,
			Year: queen.Year(), MarkingColor: string(color), MarkingColorHex: queen.MarkingColorHex(),
			ReplacementReason: reason, Notes: queen.Notes, Current: queen.IsCurrent(),
		})
	}
	return out
}

func calendarDate(value time.Time) time.Time {
	y, m, d := value.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func calendarDatePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	day := calendarDate(*value)
	return &day
}

func nonNilInspections(value []InspectionData) []InspectionData {
	if value == nil {
		return []InspectionData{}
	}
	return value
}
func nonNilHarvests(value []HarvestData) []HarvestData {
	if value == nil {
		return []HarvestData{}
	}
	return value
}
func nonNilHarvestTotals(value []HarvestTotal) []HarvestTotal {
	if value == nil {
		return []HarvestTotal{}
	}
	return value
}
