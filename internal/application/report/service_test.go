package report

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	domainhive "github.com/sbezhuk/beebase-hive-service/internal/domain/hive"
	domainqueen "github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

func TestServiceAssembleBuildsCompleteReportAndPreservesCanonicalData(t *testing.T) {
	userID, hiveID := uuid.New(), uuid.New()
	h := testHive(userID, hiveID)
	inspectionCalled := make(chan struct{}, 1)
	harvestCalled := make(chan struct{}, 1)
	released := make(chan struct{})
	close(released)
	inspection := InspectionReportResponse{
		Inspections:   []InspectionData{{ID: uuid.New(), Type: "ROUTINE", InspectedAt: "2026-06-01"}},
		ColonyHealth:  HealthData{AsOf: date("2026-06-30"), State: "GOOD", Coverage: "FULL", Dimensions: []HealthDimensionData{{Dimension: "QUEEN", State: "GOOD"}}},
		HealthHistory: HealthHistoryData{AlgorithmVersion: "v1", From: "2026-01-01", To: "2026-06-30", Points: []HealthHistoryPointData{{Date: "2026-06-01", State: "GOOD"}}},
	}
	harvest := HarvestReportResponse{
		Harvests: []HarvestData{{ID: uuid.New(), Product: "HONEY", Amount: 10, Unit: "kg", HarvestedAt: "2026-06-20"}},
		Count:    1, Totals: []HarvestTotal{{Product: "HONEY", Unit: "kg", Amount: 10}, {Product: "HONEY", Unit: "l", Amount: 4}},
	}
	service := NewService(
		fakeHives{value: &apphive.WithAccess{Hive: h}},
		fakeQueens{value: []*domainqueen.Queen{testQueen("2025-01-01", nil)}},
		fakeEntitlement{value: apphive.EntitlementPro},
		fakeInspections{value: inspection, started: inspectionCalled, release: released},
		fakeHarvests{value: harvest, started: harvestCalled, release: released},
	)
	service.now = func() time.Time { return date("2026-07-01") }

	got, err := service.Assemble(context.Background(), userID, "access", hiveID, Period{From: date("2026-01-01"), To: date("2026-06-30")}, LocaleUK)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if got.Metadata.Locale != LocaleUK || !got.Metadata.GeneratedAt.Equal(date("2026-07-01")) {
		t.Fatalf("metadata = %+v", got.Metadata)
	}
	if got.Hive.ID != hiveID || got.Hive.ApiaryID != h.ApiaryID || got.Hive.Name != h.Name {
		t.Fatalf("hive = %+v", got.Hive)
	}
	if got.Health.State != "GOOD" || got.HealthHistory.AlgorithmVersion != "v1" {
		t.Fatalf("canonical health was not preserved: health=%+v history=%+v", got.Health, got.HealthHistory)
	}
	if len(got.Inspections) != 1 || len(got.Harvests) != 1 || len(got.HarvestTotals) != 2 {
		t.Fatalf("data counts = inspections %d harvests %d totals %d", len(got.Inspections), len(got.Harvests), len(got.HarvestTotals))
	}
	if got.HarvestTotals[0].Unit == got.HarvestTotals[1].Unit && got.HarvestTotals[0].Amount+got.HarvestTotals[1].Amount == 14 {
		t.Fatal("incompatible harvest units were collapsed")
	}
	<-inspectionCalled
	<-harvestCalled
}

func TestServiceAssembleRejectsFreeBeforeDependencies(t *testing.T) {
	deps := &dependencyCalls{}
	service := NewService(
		fakeHives{value: &apphive.WithAccess{Hive: testHive(uuid.New(), uuid.New())}},
		fakeQueens{}, fakeEntitlement{value: apphive.EntitlementFree},
		fakeInspections{calls: deps}, fakeHarvests{calls: deps},
	)
	_, err := service.Assemble(context.Background(), uuid.New(), "access", uuid.New(), validPeriod(), LocaleEN)
	if !errors.Is(err, ErrProRequired) {
		t.Fatalf("error = %v, want ErrProRequired", err)
	}
	if deps.inspections != 0 || deps.harvests != 0 {
		t.Fatalf("dependency calls = %+v", deps)
	}
}

func TestServiceAssembleDoesNotCallDependenciesWhenHiveUnauthorized(t *testing.T) {
	deps := &dependencyCalls{}
	service := NewService(
		fakeHives{err: domainhive.ErrNotFound}, fakeQueens{}, fakeEntitlement{value: apphive.EntitlementPro},
		fakeInspections{calls: deps}, fakeHarvests{calls: deps},
	)
	_, err := service.Assemble(context.Background(), uuid.New(), "access", uuid.New(), validPeriod(), LocaleEN)
	if !errors.Is(err, domainhive.ErrNotFound) {
		t.Fatalf("error = %v, want hive not found", err)
	}
	if deps.inspections != 0 || deps.harvests != 0 {
		t.Fatalf("dependency calls = %+v", deps)
	}
}

func TestServiceAssembleNormalizesDependencyFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		inspectionErr error
		harvestErr    error
	}{
		{name: "inspection", inspectionErr: errors.New("connection refused")},
		{name: "harvest", harvestErr: errors.New("timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := NewService(
				fakeHives{value: &apphive.WithAccess{Hive: testHive(uuid.New(), uuid.New())}}, fakeQueens{}, fakeEntitlement{value: apphive.EntitlementPro},
				fakeInspections{err: tc.inspectionErr}, fakeHarvests{err: tc.harvestErr},
			)
			_, err := service.Assemble(context.Background(), uuid.New(), "access", uuid.New(), validPeriod(), LocaleEN)
			if !errors.Is(err, ErrReportGenerationUnavailable) {
				t.Fatalf("error = %v, want report_generation_unavailable", err)
			}
		})
	}
}

func TestServiceAssembleCollectsDependenciesConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	service := NewService(
		fakeHives{value: &apphive.WithAccess{Hive: testHive(uuid.New(), uuid.New())}}, fakeQueens{}, fakeEntitlement{value: apphive.EntitlementPro},
		fakeInspections{started: started, release: release}, fakeHarvests{started: started, release: release},
	)
	done := make(chan error, 1)
	go func() {
		_, err := service.Assemble(context.Background(), uuid.New(), "access", uuid.New(), validPeriod(), LocaleEN)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first dependency did not start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("second dependency was serialized")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
}

func TestServiceAssembleCancelsOtherDependencyOnFailure(t *testing.T) {
	canceled := make(chan struct{})
	service := NewService(
		fakeHives{value: &apphive.WithAccess{Hive: testHive(uuid.New(), uuid.New())}}, fakeQueens{}, fakeEntitlement{value: apphive.EntitlementPro},
		fakeInspections{waitForCancel: canceled}, fakeHarvests{err: errors.New("unavailable")},
	)
	_, err := service.Assemble(context.Background(), uuid.New(), "access", uuid.New(), validPeriod(), LocaleEN)
	if !errors.Is(err, ErrReportGenerationUnavailable) {
		t.Fatalf("error = %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("inspection context was not canceled")
	}
}

func TestMapQueensKeepsPeriodOverlapsAndExcludesOutsideLifecycles(t *testing.T) {
	period := Period{From: date("2026-01-01"), To: date("2026-12-31")}
	removedInside := date("2026-06-01")
	got := mapQueens([]*domainqueen.Queen{
		testQueen("2025-01-01", nil),
		testQueen("2026-02-01", nil),
		{ID: uuid.New(), MarkedAt: date("2024-01-01"), IntroducedAt: date("2024-01-01"), RemovedAt: &removedInside},
		testQueen("2027-01-01", nil),
		{ID: uuid.New(), MarkedAt: date("2024-01-01"), IntroducedAt: date("2024-01-01"), RemovedAt: func() *time.Time { v := date("2025-12-31"); return &v }()},
	}, period)
	if len(got) != 3 {
		t.Fatalf("overlapping queens = %d, want 3", len(got))
	}
	if !got[0].Current || !got[1].Current || got[2].Current || !got[2].RemovedAt.Equal(removedInside) {
		t.Fatalf("mapped queens = %+v", got)
	}
}

func TestServiceAssembleAllowsEmptyDomainData(t *testing.T) {
	service := NewService(
		fakeHives{value: &apphive.WithAccess{Hive: testHive(uuid.New(), uuid.New())}}, fakeQueens{value: []*domainqueen.Queen{}}, fakeEntitlement{value: apphive.EntitlementPro},
		fakeInspections{value: InspectionReportResponse{}}, fakeHarvests{value: HarvestReportResponse{}},
	)
	got, err := service.Assemble(context.Background(), uuid.New(), "access", uuid.New(), validPeriod(), LocaleEN)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if got.Inspections == nil || got.Harvests == nil || got.HarvestTotals == nil || got.Queens == nil {
		t.Fatal("empty collections must be non-nil")
	}
}

type fakeHives struct {
	value *apphive.WithAccess
	err   error
}

func (f fakeHives) Get(context.Context, uuid.UUID, string, uuid.UUID) (*apphive.WithAccess, error) {
	return f.value, f.err
}

type fakeQueens struct {
	value []*domainqueen.Queen
	err   error
}

func (f fakeQueens) ListHistory(context.Context, uuid.UUID, uuid.UUID) ([]*domainqueen.Queen, error) {
	return f.value, f.err
}

type fakeEntitlement struct {
	value string
	err   error
}

func (f fakeEntitlement) GetEntitlement(context.Context, string) (string, error) {
	return f.value, f.err
}

type dependencyCalls struct {
	mu                    sync.Mutex
	inspections, harvests int
}
type fakeInspections struct {
	value         InspectionReportResponse
	err           error
	calls         *dependencyCalls
	started       chan struct{}
	release       chan struct{}
	waitForCancel chan struct{}
}

func (f fakeInspections) GetReportData(ctx context.Context, _ uuid.UUID, _, _ time.Time) (InspectionReportResponse, error) {
	if f.calls != nil {
		f.calls.mu.Lock()
		f.calls.inspections++
		f.calls.mu.Unlock()
	}
	if f.started != nil {
		f.started <- struct{}{}
		<-f.release
	}
	if f.waitForCancel != nil {
		<-ctx.Done()
		close(f.waitForCancel)
	}
	return f.value, f.err
}

type fakeHarvests struct {
	value   HarvestReportResponse
	err     error
	calls   *dependencyCalls
	started chan struct{}
	release chan struct{}
}

func (f fakeHarvests) GetReportData(_ context.Context, _ uuid.UUID, _, _ time.Time) (HarvestReportResponse, error) {
	if f.calls != nil {
		f.calls.mu.Lock()
		f.calls.harvests++
		f.calls.mu.Unlock()
	}
	if f.started != nil {
		f.started <- struct{}{}
		<-f.release
	}
	return f.value, f.err
}

func testHive(userID, hiveID uuid.UUID) *domainhive.Hive {
	return &domainhive.Hive{ID: hiveID, UserID: userID, ApiaryID: uuid.New(), Name: "Hive", Notes: "Notes", CreatedAt: date("2025-01-01"), UpdatedAt: date("2026-01-01")}
}
func testQueen(introduced string, removed *time.Time) *domainqueen.Queen {
	return &domainqueen.Queen{ID: uuid.New(), MarkedAt: date("2025-01-01"), IntroducedAt: date(introduced), RemovedAt: removed}
}
func validPeriod() Period         { return Period{From: date("2026-01-01"), To: date("2026-12-31")} }
func date(value string) time.Time { parsed, _ := time.Parse("2006-01-02", value); return parsed }
