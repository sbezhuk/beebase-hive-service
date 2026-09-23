package pdf

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
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
	if zone := beeBasePalette.InsufficientDataZone(); zone == beeBasePalette.Good || zone == beeBasePalette.Watch || zone == beeBasePalette.Concern {
		t.Fatalf("insufficient-data zone reused semantic health color: %+v", zone)
	}
	if want := (RGB{248, 244, 234}); beeBasePalette.InsufficientDataZone() != want {
		t.Fatalf("insufficient-data zone = %+v, want Flutter Card-at-50%% blend %+v", beeBasePalette.InsufficientDataZone(), want)
	}
}

func TestHealthSummaryUsesClearLocalizedMetricsAndNeutralCoverage(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "uk"} {
		catalog, err := NewCatalog(locale)
		if err != nil {
			t.Fatal(err)
		}
		pdf := fpdf.New("P", "mm", "A4", "")
		pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
		pdf.AddUTF8FontFromBytes("plex", "B", renderer.bold)
		pdf.AddPage()
		doc := &document{pdf: pdf, tr: catalog, palette: beeBasePalette}
		model := sampleReport(locale, 0)
		model.Health.State = "UNKNOWN"
		model.Health.Coverage = "NONE"
		layout := doc.healthSummaryLayout(model)

		if strings.Join(layout.labels[0], " ") != catalog.T("report.overall_health") {
			t.Fatalf("%s overall label = %q", locale, strings.Join(layout.labels[0], " "))
		}
		if strings.Join(layout.labels[1], " ") != catalog.T("report.data_coverage") {
			t.Fatalf("%s coverage label = %q", locale, strings.Join(layout.labels[1], " "))
		}
		if strings.Join(layout.explanation, " ") != catalog.T("report.data_coverage_explanation") {
			t.Fatalf("%s coverage explanation = %q", locale, strings.Join(layout.explanation, " "))
		}
		if strings.Join(layout.values[0], " ") != catalog.Enum("UNKNOWN") || strings.Join(layout.values[1], " ") != catalog.Enum("NONE") {
			t.Fatalf("%s explicit states were not localized: overall=%q coverage=%q", locale, layout.values[0], layout.values[1])
		}
		if got := healthSummaryValueColor(beeBasePalette, true, "UNKNOWN"); got != beeBasePalette.Unknown {
			t.Fatalf("%s unknown color = %+v, want %+v", locale, got, beeBasePalette.Unknown)
		}
		if got := healthSummaryValueColor(beeBasePalette, false, "NONE"); got != beeBasePalette.TextPrimary {
			t.Fatalf("%s coverage color = %+v, want neutral %+v", locale, got, beeBasePalette.TextPrimary)
		}
		if got := doc.measureHealthSummary(model); got <= 0 || got > contentW {
			t.Fatalf("%s summary height = %v, want positive bounded height", locale, got)
		}
	}
}

func TestHealthSummaryWrapsLongExplanationWithinContentBounds(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
	pdf.AddUTF8FontFromBytes("plex", "B", renderer.bold)
	pdf.AddPage()
	doc := &document{pdf: pdf, tr: catalog, palette: beeBasePalette}
	lines := doc.splitText(strings.Repeat("inspection evidence and recency ", 20), contentW, "plex", "", 8)
	if len(lines) < 2 {
		t.Fatal("long health explanation did not wrap")
	}
	for _, line := range lines {
		if width := pdf.GetStringWidth(line); width > contentW+fitEpsilon {
			t.Fatalf("wrapped explanation width = %v, exceeds content width %v", width, contentW)
		}
	}
}

func TestGeneratedDateIsLocalizedDateOnly(t *testing.T) {
	instant := time.Date(2026, 9, 23, 10, 34, 56, 0, time.UTC)
	if got, want := formatTimeDate(instant, "en"), "23 Sep 2026"; got != want {
		t.Fatalf("English generated date = %q, want %q", got, want)
	}
	if got, want := formatTimeDate(instant, "uk"), "23 вер 2026"; got != want {
		t.Fatalf("Ukrainian generated date = %q, want %q", got, want)
	}
	for _, locale := range []string{"en", "uk"} {
		value := formatTimeDate(instant, locale)
		if strings.Contains(value, "10:34") || strings.Contains(value, "UTC") || strings.Contains(value, "+") || strings.Contains(value, "-") {
			t.Fatalf("%s generated date contains time or timezone data: %q", locale, value)
		}
	}
}

func TestGeneratedDatePreservesExistingCalendarDayNearTimezoneBoundary(t *testing.T) {
	localZone := time.FixedZone("UTC-4", -4*60*60)
	instant := time.Date(2026, 9, 23, 23, 59, 59, 0, localZone)
	if got, want := formatTimeDate(instant, "en"), "23 Sep 2026"; got != want {
		t.Fatalf("generated date = %q, want existing local calendar date %q", got, want)
	}
	if got := formatTimeDate(instant.UTC(), "en"); got == "23 Sep 2026" {
		t.Fatalf("boundary setup did not cross UTC date: UTC date = %q", got)
	}
}

func TestReportTableStyleUsesCanonicalBeeBaseSurfaces(t *testing.T) {
	if reportTableStyle.headerBackground != beeBasePalette.Card {
		t.Fatalf("header surface = %+v, want Card %+v", reportTableStyle.headerBackground, beeBasePalette.Card)
	}
	if reportTableStyle.bodyBackground != beeBasePalette.Background {
		t.Fatalf("body surface = %+v, want Background %+v", reportTableStyle.bodyBackground, beeBasePalette.Background)
	}
	if reportTableStyle.border != beeBasePalette.Border || reportTableStyle.borderWidth <= 0 || reportTableStyle.borderWidth >= 0.7 {
		t.Fatalf("internal table border = %+v width %v, want subtle Border", reportTableStyle.border, reportTableStyle.borderWidth)
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

func TestHealthChartHasOnlyEvaluativeStatesOnAxis(t *testing.T) {
	states := knownHealthStates()
	if len(states) != 3 || states[0] != "GOOD" || states[1] != "WATCH" || states[2] != "CONCERN" {
		t.Fatalf("health chart states = %v, want GOOD/WATCH/CONCERN", states)
	}
	if isKnownHealthState("UNKNOWN") {
		t.Fatal("UNKNOWN must not have a health-state chart coordinate")
	}
	if got := chartY(10, 60, "GOOD"); got != 14 {
		t.Fatalf("GOOD chart Y = %v, want top rail 14", got)
	}
	if got := chartY(10, 60, "WATCH"); got != 40 {
		t.Fatalf("WATCH chart Y = %v, want middle rail 40", got)
	}
	if got := chartY(10, 60, "CONCERN"); got != 66 {
		t.Fatalf("CONCERN chart Y = %v, want bottom rail 66", got)
	}
	if got := chartY(10, 60, "UNKNOWN"); !math.IsNaN(got) {
		t.Fatalf("UNKNOWN chart Y = %v, want no chart coordinate", got)
	}
}

func TestHealthChartLabelsShareCanonicalStateRows(t *testing.T) {
	vertical := healthChartVerticalBounds(10, healthChartPlotHeight)
	for _, state := range knownHealthStates() {
		stateY := chartStateY(vertical, state)
		labelCenter := healthChartLabelTopY(stateY, 1) + 3.6/2
		if math.Abs(labelCenter-stateY) > fitEpsilon {
			t.Fatalf("%s label center = %v, state Y = %v", state, labelCenter, stateY)
		}
	}
	if vertical.goodY != chartY(vertical.chartTop, healthChartPlotHeight, "GOOD") ||
		vertical.watchY != chartY(vertical.chartTop, healthChartPlotHeight, "WATCH") ||
		vertical.concernY != chartY(vertical.chartTop, healthChartPlotHeight, "CONCERN") {
		t.Fatalf("vertical geometry drifted from canonical chartY: %+v", vertical)
	}
}

func TestHealthLegendItemsMeasureIndicatorAndTextAsOneUnit(t *testing.T) {
	for _, labelWidth := range []float64{8, 20, 35} {
		want := healthLegendIndicatorWidth + healthLegendIndicatorGap + labelWidth
		if got := healthLegendItemWidth(healthLegendIndicatorWidth, healthLegendIndicatorGap, labelWidth); got != want {
			t.Fatalf("legend item width = %v, want %v", got, want)
		}
	}
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "uk"} {
		if _, err := NewCatalog(locale); err != nil {
			t.Fatal(err)
		}
		pdf := fpdf.New("P", "mm", "A4", "")
		pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
		pdf.AddPage()
		pdf.SetFont("plex", "", 7)
		center := 100.0
		baseline := pdfTextBaselineForVisualCenter(pdf, center)
		if got := pdfTextVisualCenterY(pdf, baseline); math.Abs(got-center) > fitEpsilon {
			t.Fatalf("%s legend text center = %v, want indicator center %v", locale, got, center)
		}
		if pdf.GetFontDesc("", "").Ascent == 0 {
			t.Fatalf("%s embedded font metrics were unavailable", locale)
		}
	}
}

func TestHealthChartUnknownRunsUseIndependentHalfDayZones(t *testing.T) {
	points := []report.HealthHistoryPointData{
		{Date: "2026-01-01", State: "UNKNOWN"},
		{Date: "2026-01-02", State: "GOOD"},
		{Date: "2026-01-03", State: "UNKNOWN"},
		{Date: "2026-01-04", State: "CONCERN"},
		{Date: "2026-01-05", State: "UNKNOWN"},
	}
	runs := unknownHealthRuns(points)
	if len(runs) != 3 {
		t.Fatalf("unknown runs = %v, want three independent runs", runs)
	}
	for index, want := range []healthChartRun{{start: 0, end: 0}, {start: 2, end: 2}, {start: 4, end: 4}} {
		if runs[index] != want {
			t.Fatalf("unknown run %d = %+v, want %+v", index, runs[index], want)
		}
	}
	segments := knownHealthSegments(points)
	if len(segments) != 2 || segments[0].start != 1 || segments[1].start != 3 {
		t.Fatalf("known segments = %+v, want segments starting at 1 and 3", segments)
	}
	if left, width := unknownChartBounds(runs[0], len(points)-1, contentW); left != 0 || width != contentW/8 {
		t.Fatalf("initial unknown bounds = (%v, %v), want (0, %v)", left, width, contentW/8)
	}
}

func TestHealthChartUnknownPresentationIsLocalizedAndNotRawEnum(t *testing.T) {
	for locale, want := range map[string]string{"en": "Not enough information", "uk": "Недостатньо інформації"} {
		catalog, err := NewCatalog(locale)
		if err != nil {
			t.Fatal(err)
		}
		if got := catalog.T("report.insufficient_data"); got != want {
			t.Fatalf("%s insufficient-data label = %q, want %q", locale, got, want)
		}
		if got := catalog.T("report.insufficient_data"); got == "UNKNOWN" {
			t.Fatalf("%s leaked raw UNKNOWN enum", locale)
		}
	}
}

func TestHealthChartUsesMobileStateLabelsAndNoInspectionLegend(t *testing.T) {
	for locale, want := range map[string][3]string{
		"en": {"Good", "Needs attention", "Concern"},
		"uk": {"Добре", "Потребує уваги", "Є підстави для занепокоєння"},
	} {
		catalog, err := NewCatalog(locale)
		if err != nil {
			t.Fatal(err)
		}
		for index, state := range knownHealthStates() {
			if got := catalog.T(chartStateLabelKey(state)); got != want[index] {
				t.Fatalf("%s %s label = %q, want %q", locale, state, got, want[index])
			}
		}
	}
	if got := healthLegendStates(); len(got) != 3 || got[0] != "GOOD" || got[1] != "WATCH" || got[2] != "CONCERN" {
		t.Fatalf("health legend states = %v, want exactly GOOD/WATCH/CONCERN", got)
	}
	if containsString(healthLegendStates(), "INSPECTION") {
		t.Fatal("health legend must not contain inspection")
	}
}

func TestHealthChartUsesMobileDateTickDensityAndShortLabels(t *testing.T) {
	for points, wantStep := range map[int]int{1: 2, 14: 2, 15: 5, 30: 5, 31: 7, 60: 7, 61: 13, 365: 13} {
		if got := healthChartDateStep(points); got != wantStep {
			t.Fatalf("date step for %d points = %d, want %d", points, got, wantStep)
		}
	}
	if got := formatChartDate("2026-08-02", "en", false); got != "02.08" {
		t.Fatalf("short chart date = %q, want 02.08", got)
	}
	if got := formatChartDate("2026-08-02", "uk", false); got != "02.08" {
		t.Fatalf("Ukrainian short chart date = %q, want 02.08", got)
	}
	if got := formatChartDate("2026-01-02", "en", true); got != "02.01.26" {
		t.Fatalf("cross-year chart date = %q, want 02.01.26", got)
	}
}

func TestHealthChartUsesFullSectionWidthWithDedicatedLabelZone(t *testing.T) {
	chartX, chartW := healthChartBounds()
	if chartX != contentLeft || chartW != contentW {
		t.Fatalf("chart bounds = (%v, %v), want (%v, %v)", chartX, chartW, contentLeft, contentW)
	}
	if healthChartAxisGap <= 0 || healthChartAxisGap > 5 {
		t.Fatalf("axis gap = %v, want small positive gap", healthChartAxisGap)
	}
	if healthChartDataInset <= 0 || healthChartDataInset > 2 {
		t.Fatalf("data inset = %v, want small positive inset", healthChartDataInset)
	}
}

func TestHealthChartGeometryUsesSectionEdgeAndKeepsTemporalDataInPlot(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "uk"} {
		catalog, err := NewCatalog(locale)
		if err != nil {
			t.Fatal(err)
		}
		pdf := fpdf.New("P", "mm", "A4", "")
		pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
		pdf.AddPage()
		doc := &document{pdf: pdf, tr: catalog, palette: beeBasePalette}
		geometry := doc.healthChartGeometry()
		pdf.SetFont("plex", "", 7)
		maxLabelWidth := 0.0
		for _, state := range knownHealthStates() {
			maxLabelWidth = maxFloat(maxLabelWidth, pdf.GetStringWidth(catalog.T(chartStateLabelKey(state))))
		}
		if geometry.chartLeft != contentLeft || geometry.labelZoneLeft != contentLeft {
			t.Fatalf("%s left geometry = %+v, want section-aligned chart and label zone", locale, geometry)
		}
		if got, want := geometry.labelZoneRight-geometry.labelZoneLeft, maxLabelWidth+healthChartLabelLeadingPadding+healthChartLabelTrailingPadding; math.Abs(got-want) > fitEpsilon {
			t.Fatalf("%s label zone width = %v, want measured width plus compact padding %v", locale, got, want)
		}
		if !(geometry.labelZoneLeft < geometry.labelZoneRight && geometry.labelZoneRight < geometry.plotLeft && geometry.plotLeft < geometry.plotRight) {
			t.Fatalf("%s invalid horizontal geometry = %+v", locale, geometry)
		}
		if geometry.plotRight > geometry.chartRight {
			t.Fatalf("%s plot right = %v exceeds chart right = %v", locale, geometry.plotRight, geometry.chartRight)
		}
		if healthChartAxisGap != geometry.plotLeft-geometry.labelZoneRight-healthChartDataInset {
			t.Fatalf("%s axis gap drifted: geometry=%v constant=%v", locale, geometry.plotLeft-geometry.labelZoneRight-healthChartDataInset, healthChartAxisGap)
		}
		for _, run := range unknownHealthRuns([]report.HealthHistoryPointData{
			{Date: "2026-01-01", State: "UNKNOWN"},
			{Date: "2026-01-02", State: "GOOD"},
		}) {
			left, width := unknownChartBounds(run, 1, geometry.plotRight-geometry.plotLeft)
			if geometry.plotLeft+left < geometry.plotLeft || geometry.plotLeft+left+width > geometry.plotRight {
				t.Fatalf("%s UNKNOWN bounds escaped plot: left=%v width=%v geometry=%+v", locale, left, width, geometry)
			}
		}
	}
}

func TestHealthChartUnknownFixturesRenderWithoutChangingHistoryData(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string][]string{
		"initial":  {"UNKNOWN", "UNKNOWN", "CONCERN", "WATCH"},
		"middle":   {"GOOD", "UNKNOWN", "UNKNOWN", "WATCH"},
		"trailing": {"GOOD", "WATCH", "UNKNOWN"},
		"multiple": {"UNKNOWN", "GOOD", "UNKNOWN", "CONCERN", "UNKNOWN"},
		"all":      {"UNKNOWN", "UNKNOWN", "UNKNOWN"},
	}
	for name, states := range fixtures {
		model := sampleReport("en", 0)
		model.HealthHistory.Points = make([]report.HealthHistoryPointData, len(states))
		for index, state := range states {
			model.HealthHistory.Points[index] = report.HealthHistoryPointData{Date: fmt.Sprintf("2026-01-%02d", index+1), State: state}
		}
		content, err := renderer.Render(context.Background(), model)
		if err != nil {
			t.Fatalf("%s fixture Render() error = %v", name, err)
		}
		assertPDF(t, content)
		if dir := os.Getenv("REPORT_SAMPLE_DIR"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, "health-history-"+name+".pdf"), content, 0o600); err != nil {
				t.Fatal(err)
			}
		}
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

func TestReportTablesUseSafeContentWidth(t *testing.T) {
	for name, widths := range map[string][]float64{
		"health":         healthTableWidths(),
		"inspection":     inspectionTableWidths(),
		"queen":          queenTableWidths(),
		"harvest":        harvestTableWidths(),
		"harvest totals": harvestTotalWidths(),
	} {
		var total float64
		for _, width := range widths {
			if width <= 0 {
				t.Fatalf("%s has non-positive column width %v", name, width)
			}
			total += width
		}
		if total != contentW {
			t.Fatalf("%s width = %v, want content width %v", name, total, contentW)
		}
	}
}

func TestTableColumnBoundariesUseCanonicalWidths(t *testing.T) {
	for name, widths := range map[string][]float64{
		"health":     healthTableWidths(),
		"inspection": inspectionTableWidths(),
		"queen":      queenTableWidths(),
		"harvest":    harvestTableWidths(),
		"summary":    []float64{100, contentW - 100},
	} {
		boundaries := tableColumnBoundaries(widths)
		if len(boundaries) != len(widths)-1 {
			t.Fatalf("%s boundaries = %d, want %d", name, len(boundaries), len(widths)-1)
		}
		previous := contentLeft
		for _, boundary := range boundaries {
			if boundary <= previous || boundary >= contentRight {
				t.Fatalf("%s boundary %v is outside table bounds (%v, %v)", name, boundary, contentLeft, contentRight)
			}
			previous = boundary
		}
	}
}

func TestSectionSpacingIsCentralizedAndPaginationAware(t *testing.T) {
	if sectionGapBefore <= sectionContentGap {
		t.Fatalf("section gap %v must exceed title-to-content gap %v", sectionGapBefore, sectionContentGap)
	}
	if sectionTitleHeight <= 0 || sectionContentGap <= 0 {
		t.Fatalf("invalid section rhythm: title=%v content gap=%v", sectionTitleHeight, sectionContentGap)
	}
	if 18+sectionTitleHeight+sectionContentGap+sectionGapBefore >= pageHeight {
		t.Fatal("section spacing does not leave safe page content bounds")
	}
}

func TestTablePaginationKeepsHeaderWithFirstRow(t *testing.T) {
	const (
		headerHeight = 10.0
		rowHeight    = 14.0
	)
	if !tableStartFits(250, headerHeight, rowHeight) {
		t.Fatal("table should fit when header and first row fit together")
	}
	if tableStartFits(pageHeight-18-headerHeight, headerHeight, rowHeight) {
		t.Fatal("table should move when only the header fits")
	}
	if !tableStartFits(pageHeight-18-headerHeight-rowHeight, headerHeight, rowHeight) {
		t.Fatal("exact header plus first-row boundary should fit")
	}
	if tableRowFits(pageHeight-18-1, 2) {
		t.Fatal("body row should move when it does not fit in the remaining space")
	}
}

func TestSectionStartFitsKeepsMeasuredTableBlockTogether(t *testing.T) {
	const (
		gap         = sectionGapBefore
		title       = sectionTitleHeight
		contentGap  = sectionContentGap
		header      = 8.0
		firstRow    = 16.0
		currentPage = pageHeight - 18 - gap - title - contentGap - header - firstRow
	)
	if !sectionStartFits(currentPage, header+firstRow, true) {
		t.Fatal("exact section title and first table block boundary should fit")
	}
	if sectionStartFits(currentPage+0.01, header+firstRow, true) {
		t.Fatal("section title should move when the measured first table block no longer fits")
	}
}

func TestKeepWithNextMovesIntroducerWhenOnlyItFits(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
	pdf.AddPage()
	doc := &document{pdf: pdf, palette: beeBasePalette}

	pdf.SetY(contentBottom - 16)
	if moved := doc.ensureBlockStartFits(8, 0, 8); moved {
		t.Fatal("block that fits exactly should remain on the current page")
	}
	if pdf.PageNo() != 1 {
		t.Fatalf("page number = %d, want 1", pdf.PageNo())
	}

	pdf.SetY(contentBottom - 10)
	if moved := doc.ensureBlockStartFits(8, 0, 8); !moved {
		t.Fatal("introducer that fits without its child should move to the next page")
	}
	if pdf.PageNo() != 2 {
		t.Fatalf("page number = %d, want 2", pdf.PageNo())
	}
}

func TestKeepWithNextDoesNotLoopForOversizedChild(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
	pdf.AddPage()
	doc := &document{pdf: pdf, palette: beeBasePalette}

	pdf.SetY(margin)
	if moved := doc.ensureBlockStartFits(8, 0, contentBottom-margin+20); moved {
		t.Fatal("oversized child on a fresh page must not trigger a second blank page")
	}
	if pdf.PageNo() != 1 {
		t.Fatalf("page number = %d, want 1", pdf.PageNo())
	}
}

func TestHarvestTotalKeepsLabelWithMeasuredFirstRow(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	tr, err := NewCatalog("en")
	if err != nil {
		t.Fatal(err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
	pdf.AddUTF8FontFromBytes("plex", "B", renderer.bold)
	pdf.AddPage()
	doc := &document{pdf: pdf, tr: tr, palette: beeBasePalette}
	total := report.HarvestTotal{Product: "HONEY", Amount: 10, Unit: "kg"}
	doc.setBody()
	_, firstRowHeight := doc.measureWrappedRow(doc.harvestTotalRowValues(total), harvestTotalWidths(), rowLineH, tablePaddingY, tableMinRowHeight)
	pdf.SetY(contentBottom - totalBlockGap - totalLabelHeight - 1)
	if !blockStartFits(pdf.GetY(), totalBlockGap+totalLabelHeight, 0, firstRowHeight) {
		// The label alone still fits, which is the regression condition.
		if pdf.GetY()+totalBlockGap+totalLabelHeight > contentBottom+fitEpsilon {
			t.Fatal("test setup does not leave room for the Total label")
		}
	} else {
		t.Fatal("test setup unexpectedly fits the Total label and first row")
	}
	if moved := doc.ensureBlockStartFits(totalBlockGap+totalLabelHeight, 0, firstRowHeight); !moved {
		t.Fatal("Total label should move with its first totals row")
	}
}

func TestMeasureTableStartIncludesWrappedFirstRow(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
	pdf.AddUTF8FontFromBytes("plex", "B", renderer.bold)
	pdf.AddPage()
	doc := &document{pdf: pdf, palette: beeBasePalette}
	short := doc.measureTableStart([]string{"Date", "Type", "Assessment"}, inspectionTableWidths(), []string{"2026-09-01", "ROUTINE", "Short"}, rowLineH)
	long := doc.measureTableStart([]string{"Date", "Type", "Assessment"}, inspectionTableWidths(), []string{"2026-09-01", "ROUTINE", strings.Repeat("Long assessment text ", 20)}, rowLineH)
	if long <= short {
		t.Fatalf("wrapped first row height = %v, short row height = %v; expected measured growth", long, short)
	}
}

func TestDisplayCellValueUsesPlaceholderOnlyForMissingText(t *testing.T) {
	for _, value := range []string{"", " ", "\t\n"} {
		if got := displayCellValue(value); got != emptyCellPlaceholder {
			t.Fatalf("displayCellValue(%q) = %q, want %q", value, got, emptyCellPlaceholder)
		}
	}
	for _, value := range []string{"0", "0.00", "No", "None", "Unknown"} {
		if got := displayCellValue(value); got != value {
			t.Fatalf("displayCellValue(%q) = %q, want unchanged value", value, got)
		}
	}
}

func TestTablePaddingStaysInsideCellBounds(t *testing.T) {
	if tablePaddingX < 2 || tablePaddingX > 3 || tablePaddingY < 1.5 || tablePaddingY > 2 {
		t.Fatalf("unexpected table padding: horizontal=%v vertical=%v", tablePaddingX, tablePaddingY)
	}
	for _, width := range inspectionTableWidths() {
		if width-2*tablePaddingX <= 0 {
			t.Fatalf("column width %v cannot contain canonical horizontal padding", width)
		}
	}
	if tableMinRowHeight < rowLineH+2*tablePaddingY {
		t.Fatalf("minimum row height %v does not include vertical padding", tableMinRowHeight)
	}
}

func TestWrapCellUsesFontMetricsForEnglishAndUkrainian(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("plex", "", renderer.regular)
	pdf.AddPage()
	pdf.SetFont("plex", "", 9)
	doc := &document{pdf: pdf}
	for locale, value := range map[string]string{
		"en": "Colony strength: Moderate; Queen status: Problem; Brood status: Not checked; Food stores: Low; Health concerns: Varroa signs; Feeding performed: No",
		"uk": "Сила сім'ї: Середня; Стан матки: Проблема; Стан розплоду: Не перевірено; Запаси корму: Низькі; Проблеми зі здоров'ям: ознаки вароа; Годівля: Ні",
	} {
		lines := doc.wrapCell(value, 70)
		if len(lines) < 2 {
			t.Fatalf("%s content did not wrap", locale)
		}
		for _, line := range lines {
			if got := pdf.GetStringWidth(line); got > 65.001 {
				t.Fatalf("%s line width = %v, exceeds inner width", locale, got)
			}
		}
	}
}

func TestRendererHandlesPaddedLongTableRows(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	model := sampleReport("uk", 20)
	long := "Сила сім'ї: Середня; Стан матки: Проблема; Стан розплоду: Не перевірено; Запаси корму: Низькі; Проблеми зі здоров'ям: ознаки вароа; Годівля: не виконувалась; Додаткове спостереження для перевірки переносу тексту"
	model.Inspections[0].Assessment = &report.AssessmentData{HealthConcerns: stringPtr(long)}
	model.Health.Dimensions[0].Sources = []report.HealthEvidenceSourceData{
		{InspectedAt: "2026-06-01"},
		{InspectedAt: "2026-07-01"},
		{InspectedAt: "2026-08-01"},
		{InspectedAt: "2026-09-01"},
	}
	content, err := renderer.Render(context.Background(), model)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	assertPDF(t, content)
	if strings.Count(string(content), "/Type /Page") < 2 {
		t.Fatal("padded long rows did not paginate")
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

func TestRendererProducesUkrainianHealthHistoryChart(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	model := sampleReport("uk", 0)
	model.HealthHistory.Points = []report.HealthHistoryPointData{
		{Date: "2026-08-02", State: "UNKNOWN"},
		{Date: "2026-08-03", State: "GOOD"},
		{Date: "2026-08-04", State: "GOOD"},
		{Date: "2026-08-05", State: "WATCH"},
		{Date: "2026-08-06", State: "CONCERN"},
	}
	content, err := renderer.Render(context.Background(), model)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	assertPDF(t, content)
	if dir := os.Getenv("REPORT_SAMPLE_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "hive-report-uk-chart.pdf"), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRendererProducesHealthSummaryStatePreviews(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name     string
		state    string
		coverage string
	}{
		{name: "watch-high", state: "WATCH", coverage: "HIGH"},
		{name: "unknown-none", state: "UNKNOWN", coverage: "NONE"},
	} {
		model := sampleReport("en", 0)
		model.Health.State = fixture.state
		model.Health.Coverage = fixture.coverage
		content, err := renderer.Render(context.Background(), model)
		if err != nil {
			t.Fatalf("%s Render() error = %v", fixture.name, err)
		}
		assertPDF(t, content)
		if dir := os.Getenv("REPORT_SAMPLE_DIR"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, "hive-report-"+fixture.name+".pdf"), content, 0o600); err != nil {
				t.Fatal(err)
			}
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
