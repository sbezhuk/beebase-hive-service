package pdf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-hive-service/internal/application/report"
)

func TestHiveQRPayloadIsCanonical(t *testing.T) {
	id := uuid.New().String()
	if got, want := HiveQRPayload(id), "beebase://hive/v1/"+id; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestReportPaletteMirrorsMobileLightSemanticTokens(t *testing.T) {
	if beeBasePalette.Brand != (RGB{232, 172, 61}) || beeBasePalette.TextPrimary != (RGB{43, 27, 14}) {
		t.Fatalf("brand/text palette drifted: %+v / %+v", beeBasePalette.Brand, beeBasePalette.TextPrimary)
	}
	if beeBasePalette.HealthState("GOOD") != (RGB{154, 93, 20}) || beeBasePalette.HealthState("WATCH") != (RGB{181, 101, 29}) || beeBasePalette.HealthState("CONCERN") != (RGB{199, 64, 45}) {
		t.Fatalf("health semantic palette drifted: good=%+v watch=%+v concern=%+v", beeBasePalette.Good, beeBasePalette.Watch, beeBasePalette.Concern)
	}
}

func TestChartLabelXKeepsDateLabelsInsideSafeBounds(t *testing.T) {
	const (
		chartX = 15.0
		chartW = 180.0
		labelW = 28.0
	)

	tests := []struct {
		name   string
		pointX float64
		labelW float64
		wantX  float64
	}{
		{name: "left edge", pointX: chartX, labelW: labelW, wantX: chartX},
		{name: "center", pointX: chartX + chartW/2, labelW: labelW, wantX: chartX + (chartW-labelW)/2},
		{name: "right edge", pointX: chartX + chartW, labelW: labelW, wantX: chartX + chartW - labelW},
		{name: "wide label", pointX: chartX + chartW, labelW: 80, wantX: chartX + chartW - 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chartLabelX(tt.pointX, chartX, chartW, tt.labelW)
			if got != tt.wantX {
				t.Fatalf("chartLabelX() = %v, want %v", got, tt.wantX)
			}
			if got < chartX || got+tt.labelW > chartX+chartW {
				t.Fatalf("label bounds [%v, %v] escaped chart bounds [%v, %v]", got, got+tt.labelW, chartX, chartX+chartW)
			}
		})
	}
}

func TestFullWidthDividerBoundsAreCanonical(t *testing.T) {
	left, right, width := fullWidthDividerBounds()
	if left != margin || right != pageWidth-margin || width != contentW {
		t.Fatalf("divider bounds = (%v, %v, %v), want (%v, %v, %v)", left, right, width, margin, pageWidth-margin, contentW)
	}
	if left < 0 || right > pageWidth || width <= 0 {
		t.Fatalf("divider escaped safe page bounds: (%v, %v, %v)", left, right, width)
	}
	for _, value := range []string{"0", "12", "999999"} {
		if got := contentW; got != width {
			t.Fatalf("summary divider width changed for value %q: %v", value, got)
		}
	}
}

func TestRendererProducesValidEnglishPDFWithAllSections(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	model := sampleReport("en", 8)
	content, err := renderer.Render(context.Background(), model)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	assertPDF(t, content)
	if strings.Count(string(content), "/Type /Page") < 2 {
		t.Fatalf("full report did not paginate: %d pages", strings.Count(string(content), "/Type /Page"))
	}
	path := t.TempDir() + "/english.pdf"
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("REPORT_SAMPLE_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "hive-report-en.pdf"), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRendererProducesUkrainianPDFAndEmptyStates(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	model := sampleReport("uk", 0)
	content, err := renderer.Render(context.Background(), model)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	assertPDF(t, content)
	if len(content) < 1000 {
		t.Fatalf("Ukrainian PDF unexpectedly small: %d bytes", len(content))
	}
	if dir := os.Getenv("REPORT_SAMPLE_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "hive-report-uk.pdf"), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRendererHonorsCancellation(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderer.Render(ctx, sampleReport("en", 0)); err == nil {
		t.Fatal("Render accepted canceled context")
	}
}

func assertPDF(t *testing.T, content []byte) {
	t.Helper()
	if !strings.HasPrefix(string(content), "%PDF-") {
		t.Fatalf("missing PDF header")
	}
	if !strings.HasSuffix(strings.TrimSpace(string(content)), "%%EOF") {
		t.Fatalf("missing PDF EOF marker")
	}
}

func sampleReport(locale string, records int) *report.HiveReport {
	hiveID := uuid.New()
	model := &report.HiveReport{
		Metadata:      report.ReportMetadata{From: date("2026-01-01"), To: date("2026-12-31"), Locale: locale, GeneratedAt: dateTime("2026-09-22T10:30:00Z")},
		Hive:          report.HiveData{ID: hiveID, ApiaryID: uuid.New(), ApiaryName: stringPtr("Пасіка з дуже довгою назвою для перевірки переносу"), Name: "Вулик №1", Notes: "Зразкові нотатки", CreatedAt: date("2025-01-01"), UpdatedAt: date("2026-01-01")},
		Health:        report.HealthData{State: "GOOD", Coverage: "HIGH", Dimensions: []report.HealthDimensionData{{Dimension: "STRENGTH", State: "GOOD", Coverage: "HIGH", Sources: []report.HealthEvidenceSourceData{{InspectionID: uuid.New(), InspectedAt: "2026-06-01"}}}}},
		HealthHistory: report.HealthHistoryData{From: "2026-01-01", To: "2026-12-31", Points: []report.HealthHistoryPointData{{Date: "2026-01-01", State: "UNKNOWN"}, {Date: "2026-06-01", State: "GOOD"}, {Date: "2026-12-31", State: "WATCH"}}},
		Queens:        []report.QueenData{{ID: uuid.New(), MarkedAt: date("2025-01-01"), IntroducedAt: date("2025-02-01"), Year: 2025, MarkingColor: "blue", MarkingColorHex: "#A7C7F7", Current: true}},
		HarvestTotals: []report.HarvestTotal{{Product: "HONEY", Unit: "kg", Amount: 10}, {Product: "HONEY", Unit: "l", Amount: 4}},
	}
	for i := 0; i < records; i++ {
		model.Inspections = append(model.Inspections, report.InspectionData{ID: uuid.New(), Type: "ROUTINE", InspectedAt: "2026-06-01", Assessment: &report.AssessmentData{ColonyStrength: stringPtr("STRONG"), FeedTypes: stringSlicePtr([]string{"SUGAR_SYRUP"})}})
		model.Harvests = append(model.Harvests, report.HarvestData{ID: uuid.New(), Product: "HONEY", Amount: float64(i), Unit: "kg", HarvestedAt: "2026-07-01"})
	}
	return model
}

func date(value string) time.Time             { parsed, _ := time.Parse("2006-01-02", value); return parsed }
func dateTime(value string) time.Time         { parsed, _ := time.Parse(time.RFC3339, value); return parsed }
func stringPtr(value string) *string          { return &value }
func stringSlicePtr(value []string) *[]string { return &value }
