package pdf

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/sbezhuk/beebase-hive-service/internal/application/report"
)

//go:embed fonts/IBMPlexSans-Regular.ttf fonts/IBMPlexSans-Bold.ttf
var fontFiles embed.FS

var ErrRender = errors.New("report render failed")

const (
	pageWidth                       = 210.0
	pageHeight                      = 297.0
	margin                          = 15.0
	contentLeft                     = margin
	contentRight                    = pageWidth - margin
	contentW                        = contentRight - contentLeft
	tablePaddingX                   = 2.5
	tablePaddingY                   = 1.5
	tableHeaderPaddingY             = 1.5
	tableMinRowHeight               = 8.0
	rowLineH                        = 5.0
	sectionGapBefore                = 7.0
	sectionTitleHeight              = 8.0
	sectionContentGap               = 4.0
	contentBottom                   = pageHeight - 18
	fitEpsilon                      = 0.0001
	healthChartHeight               = 76.0
	healthChartPlotHeight           = 52.0
	healthChartPlotInset            = 4.0
	healthChartDataInset            = 1.5
	healthChartLabelLeadingPadding  = 1.0
	healthChartLabelTrailingPadding = 0.5
	healthChartAxisGap              = 2.0
	healthChartDateGap              = 5.0
	healthChartLegendGap            = 7.0
	healthChartLegendHeight         = 6.0
	healthLegendIndicatorWidth      = 5.0
	healthLegendIndicatorGap        = 2.0
	healthLegendItemGap             = 7.0
	healthLegendIndicatorCenter     = 2.5
	hiveQRCodeSize                  = 34.0
	hiveQRCaptionWidth              = 36.0
	totalBlockGap                   = 2.0
	totalLabelHeight                = 6.0
	healthMetricGap                 = 10.0
	healthLabelLineH                = 4.0
	healthValueLineH                = 6.0
	healthExplanationGap            = 2.0
	healthExplanationLineH          = 4.0
)

type Renderer struct {
	regular       []byte
	bold          []byte
	regularMetric ttfMetrics
	boldMetric    ttfMetrics
}

type tableStyle struct {
	headerBackground RGB
	bodyBackground   RGB
	border           RGB
	borderWidth      float64
}

var reportTableStyle = tableStyle{
	headerBackground: beeBasePalette.Card,
	bodyBackground:   beeBasePalette.Background,
	border:           beeBasePalette.Border,
	borderWidth:      0.25,
}

func NewRenderer() (*Renderer, error) {
	regular, err := fontFiles.ReadFile("fonts/IBMPlexSans-Regular.ttf")
	if err != nil {
		return nil, fmt.Errorf("load regular report font: %w", err)
	}
	bold, err := fontFiles.ReadFile("fonts/IBMPlexSans-Bold.ttf")
	if err != nil {
		return nil, fmt.Errorf("load bold report font: %w", err)
	}
	regularMetric, err := parseTTFMetrics(regular)
	if err != nil {
		return nil, fmt.Errorf("parse regular report font metrics: %w", err)
	}
	boldMetric, err := parseTTFMetrics(bold)
	if err != nil {
		return nil, fmt.Errorf("parse bold report font metrics: %w", err)
	}
	return &Renderer{regular: regular, bold: bold, regularMetric: regularMetric, boldMetric: boldMetric}, nil
}

func (r *Renderer) Render(ctx context.Context, model *report.HiveReport) ([]byte, error) {
	if model == nil {
		return nil, fmt.Errorf("%w: report model is nil", ErrRender)
	}
	tr, err := NewCatalog(model.Metadata.Locale)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRender, err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(true)
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, 14)
	pdf.AddUTF8FontFromBytes("plex", "", r.regular)
	pdf.AddUTF8FontFromBytes("plex", "B", r.bold)
	pdf.SetTitle(tr.T("report.title"), false)
	pdf.SetAuthor("BeeBase", false)
	pdf.SetFooterFunc(func() {
		pdf.SetY(pageHeight - 10)
		pdf.SetFont("plex", "", 8)
		setTextColor(pdf, beeBasePalette.TextSecondary)
		pdf.CellFormat(contentW, 5, strconv.Itoa(pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()
	doc := &document{
		pdf:           pdf,
		tr:            tr,
		ctx:           ctx,
		palette:       beeBasePalette,
		regularMetric: r.regularMetric,
		boldMetric:    r.boldMetric,
	}
	if err := doc.header(model); err != nil {
		return nil, err
	}
	if err := doc.health(model); err != nil {
		return nil, err
	}
	if err := doc.history(model); err != nil {
		return nil, err
	}
	if err := doc.inspections(model); err != nil {
		return nil, err
	}
	if err := doc.queens(model); err != nil {
		return nil, err
	}
	if err := doc.harvests(model); err != nil {
		return nil, err
	}
	if err := doc.summary(model); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRender, err)
	}
	var output bytes.Buffer
	if err := pdf.Output(&output); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRender, err)
	}
	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRender, err)
	}
	return output.Bytes(), nil
}

// HiveQRPayload is the stable deep-link encoded in every report QR code.
func HiveQRPayload(hiveID string) string { return "beebase://hive/v1/" + hiveID }

type document struct {
	pdf           *fpdf.Fpdf
	tr            Catalog
	ctx           context.Context
	palette       ReportPalette
	regularMetric ttfMetrics
	boldMetric    ttfMetrics
	sections      int
	table         *tableState
}

type tableState struct {
	headers      []string
	headerLines  [][]string
	widths       []float64
	lineHeight   float64
	verticalPad  float64
	minRowHeight float64
	headerHeight float64
	bodyRows     int
}

func (d *document) check() error {
	if err := d.ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrRender, err)
	}
	return nil
}

func (d *document) setBody() {
	d.pdf.SetFont("plex", "", 9)
	setTextColor(d.pdf, d.palette.TextPrimary)
}

func (d *document) header(model *report.HiveReport) error {
	if err := d.check(); err != nil {
		return err
	}
	setTextColor(d.pdf, d.palette.TextPrimary)
	d.pdf.SetFont("plex", "B", 23)
	d.opticalCell(125, 10, d.tr.T("report.title"), "B", 1)
	if model.Hive.ApiaryName != nil && strings.TrimSpace(*model.Hive.ApiaryName) != "" {
		d.pdf.SetFont("plex", "B", 12)
		setTextColor(d.pdf, d.palette.TextSecondary)
		d.opticalMultiCell(125, 6, strings.TrimSpace(*model.Hive.ApiaryName), "B")
	}
	d.pdf.SetFont("plex", "B", 14)
	setTextColor(d.pdf, d.palette.TextPrimary)
	d.opticalMultiCell(125, 7, model.Hive.Name, "B")
	setDrawColor(d.pdf, d.palette.Border)
	d.fullWidthDivider(d.pdf.GetY() + 2)
	d.pdf.SetY(d.pdf.GetY() + 8)
	d.setBody()
	d.opticalCell(37, 6, d.tr.T("report.period"), "", 0)
	d.pdf.CellFormat(70, 6, formatDate(model.Metadata.From.String(), d.tr.Locale)+" - "+formatDate(model.Metadata.To.String(), d.tr.Locale), "", 1, "L", false, 0, "")
	d.opticalCell(37, 6, d.tr.T("report.generated_at"), "", 0)
	d.pdf.CellFormat(70, 6, formatTimeDate(model.Metadata.GeneratedAt, d.tr.Locale), "", 1, "L", false, 0, "")
	if model.Hive.Notes != "" {
		d.pdf.SetX(contentLeft + 37)
		d.pdf.MultiCell(100, 6, model.Hive.Notes, "", "L", false)
	}
	if err := d.qr(model.Hive.ID.String()); err != nil {
		return err
	}
	d.pdf.Ln(5)
	return nil
}

func (d *document) qr(hiveID string) error {
	contentY := d.pdf.GetY()
	payload := HiveQRPayload(hiveID)
	image, err := qrcode.Encode(payload, qrcode.Medium, 180)
	if err != nil {
		return fmt.Errorf("%w: generate hive QR: %v", ErrRender, err)
	}
	d.pdf.RegisterImageOptionsReader("hive-qr", fpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, bytes.NewReader(image))
	qrX, captionX := hiveQRBounds()
	y := margin + 5
	d.pdf.ImageOptions("hive-qr", qrX, y, hiveQRCodeSize, hiveQRCodeSize, false, fpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, 0, "")
	d.pdf.SetXY(captionX, y+hiveQRCodeSize+1)
	d.pdf.SetFont("plex", "", 7)
	setTextColor(d.pdf, d.palette.TextSecondary)
	d.pdf.MultiCell(hiveQRCaptionWidth, 3, d.tr.T("report.qr_instruction"), "", "C", false)
	d.pdf.SetY(contentY)
	d.setBody()
	return nil
}

func hiveQRBounds() (qrX, captionX float64) {
	return contentRight - hiveQRCodeSize, contentRight - hiveQRCaptionWidth
}

func (d *document) section(title string, minimumContentHeight float64) error {
	if err := d.check(); err != nil {
		return err
	}
	gap := 0.0
	if d.sections > 0 {
		gap = sectionGapBefore
	}
	if d.ensureBlockStartFits(gap+sectionTitleHeight, sectionContentGap, minimumContentHeight) {
		gap = 0
	}
	if gap > 0 {
		d.pdf.Ln(gap)
	}
	setTextColor(d.pdf, d.palette.TextPrimary)
	d.pdf.SetFont("plex", "B", 15)
	d.pdf.SetX(contentLeft)
	d.opticalCell(contentW, sectionTitleHeight, title, "B", 1)
	d.fullWidthDivider(d.pdf.GetY())
	d.pdf.Ln(sectionContentGap)
	d.setBody()
	d.sections++
	return nil
}

func (d *document) fullWidthDivider(y float64) {
	setDrawColor(d.pdf, d.palette.Brand)
	d.pdf.SetLineWidth(0.7)
	d.pdf.Line(contentLeft, y, contentRight, y)
}

func fullWidthDividerBounds() (left, right, width float64) {
	return contentLeft, contentRight, contentW
}

func (d *document) health(model *report.HiveReport) error {
	widths := healthTableWidths()
	headers := []string{d.tr.T("report.health_dimension"), d.tr.T("report.state"), d.tr.T("report.coverage"), d.tr.T("report.health_evidence")}
	minimumContentHeight := d.measureHealthSummary(model)
	if len(model.Health.Dimensions) > 0 {
		minimumContentHeight += d.measureTableStart(headers, widths, d.healthRowValues(model.Health.Dimensions[0]), rowLineH)
	}
	if err := d.section(d.tr.T("report.colony_health"), minimumContentHeight); err != nil {
		return err
	}
	d.renderHealthSummary(model)
	d.tableHeader(headers, widths)
	for _, dimension := range model.Health.Dimensions {
		d.row(d.healthRowValues(dimension), widths, rowLineH)
	}
	d.pdf.Ln(4)
	return nil
}

type healthSummaryLayout struct {
	labels            [2][]string
	values            [2][]string
	metricHeight      float64
	explanation       []string
	explanationHeight float64
}

func (d *document) healthSummaryLayout(model *report.HiveReport) healthSummaryLayout {
	width := (contentW - healthMetricGap) / 2
	layout := healthSummaryLayout{
		labels: [2][]string{
			d.splitText(d.tr.T("report.overall_health"), width, "plex", "", 8),
			d.splitText(d.tr.T("report.data_coverage"), width, "plex", "", 8),
		},
		values: [2][]string{
			d.splitText(d.tr.Enum(model.Health.State), width, "plex", "B", 11),
			d.splitText(d.tr.Enum(model.Health.Coverage), width, "plex", "B", 11),
		},
		explanation: d.splitText(d.tr.T("report.data_coverage_explanation"), contentW, "plex", "", 8),
	}
	labelHeight := maxFloat(float64(len(layout.labels[0]))*healthLabelLineH, float64(len(layout.labels[1]))*healthLabelLineH)
	valueHeight := maxFloat(float64(len(layout.values[0]))*healthValueLineH, float64(len(layout.values[1]))*healthValueLineH)
	layout.metricHeight = labelHeight + valueHeight
	layout.explanationHeight = float64(len(layout.explanation)) * healthExplanationLineH
	return layout
}

func (d *document) measureHealthSummary(model *report.HiveReport) float64 {
	layout := d.healthSummaryLayout(model)
	return layout.metricHeight + healthExplanationGap + layout.explanationHeight
}

func (d *document) renderHealthSummary(model *report.HiveReport) {
	layout := d.healthSummaryLayout(model)
	width := (contentW - healthMetricGap) / 2
	startY := d.pdf.GetY()
	labelHeight := layout.metricHeight - maxFloat(float64(len(layout.values[0]))*healthValueLineH, float64(len(layout.values[1]))*healthValueLineH)
	for index, x := range []float64{contentLeft, contentLeft + width + healthMetricGap} {
		d.pdf.SetXY(x, startY)
		d.pdf.SetFont("plex", "", 8)
		setTextColor(d.pdf, d.palette.TextSecondary)
		d.pdf.MultiCell(width, healthLabelLineH, strings.Join(layout.labels[index], "\n"), "", "L", false)
		d.pdf.SetXY(x, startY+labelHeight)
		d.pdf.SetFont("plex", "B", 11)
		if index == 0 {
			setTextColor(d.pdf, healthSummaryValueColor(d.palette, true, model.Health.State))
		} else {
			setTextColor(d.pdf, healthSummaryValueColor(d.palette, false, model.Health.Coverage))
		}
		d.pdf.MultiCell(width, healthValueLineH, strings.Join(layout.values[index], "\n"), "", "L", false)
	}
	d.pdf.SetXY(contentLeft, startY+layout.metricHeight+healthExplanationGap)
	d.pdf.SetFont("plex", "", 8)
	setTextColor(d.pdf, d.palette.TextSecondary)
	d.opticalMultiCell(contentW, healthExplanationLineH, strings.Join(layout.explanation, "\n"), "")
	d.pdf.SetY(startY + d.measureHealthSummary(model))
	d.setBody()
}

func healthSummaryValueColor(palette ReportPalette, overall bool, value string) RGB {
	if overall {
		return palette.HealthState(value)
	}
	return palette.TextPrimary
}

func (d *document) splitText(value string, width float64, family, style string, size float64) []string {
	d.pdf.SetFont(family, style, size)
	lines := d.pdf.SplitLines([]byte(value), width)
	if len(lines) == 0 {
		return []string{""}
	}
	result := make([]string, len(lines))
	for index, line := range lines {
		result[index] = string(line)
	}
	return result
}

func (d *document) healthRowValues(dimension report.HealthDimensionData) []string {
	evidence := make([]string, 0, len(dimension.Sources))
	for _, source := range dimension.Sources {
		evidence = append(evidence, formatDate(source.InspectedAt, d.tr.Locale))
	}
	return []string{d.tr.Enum(dimension.Dimension), d.tr.Enum(dimension.State), d.tr.Enum(dimension.Coverage), strings.Join(evidence, ", ")}
}

func (d *document) history(model *report.HiveReport) error {
	minimumContentHeight := healthChartHeight
	if len(model.HealthHistory.Points) == 0 {
		minimumContentHeight = d.measureEmptyState(d.tr.T("report.no_health_history"))
	}
	if err := d.section(d.tr.T("report.health_history"), minimumContentHeight); err != nil {
		return err
	}
	if len(model.HealthHistory.Points) == 0 {
		d.empty(d.tr.T("report.no_health_history"))
		return nil
	}
	d.chart(model.HealthHistory.Points)
	d.pdf.Ln(4)
	return nil
}

func (d *document) chart(points []report.HealthHistoryPointData) {
	y := d.pdf.GetY()
	geometry := d.healthChartGeometry()
	dataX := geometry.plotLeft
	dataW := geometry.plotRight - geometry.plotLeft
	h := healthChartPlotHeight
	vertical := healthChartVerticalBounds(y, h)
	maxIndex := len(points) - 1
	xForIndex := func(index int) float64 {
		return dataX + dataW*float64(index)/float64(maxInt(1, maxIndex))
	}

	// UNKNOWN has no health-state coordinate. Paint its calendar-day interval
	// first, using the same half-slot expansion as the Flutter chart, so the
	// neutral region—not a fabricated fourth severity—explains the gap.
	for _, run := range unknownHealthRuns(points) {
		left, width := unknownChartBounds(run, maxIndex, dataW)
		setFillColor(d.pdf, d.palette.InsufficientDataZone())
		d.pdf.Rect(dataX+left, vertical.goodY, width, vertical.concernY-vertical.goodY, "F")
	}

	setDrawColor(d.pdf, d.palette.Border)
	d.pdf.SetLineWidth(0.25)
	for _, state := range knownHealthStates() {
		yy := chartStateY(vertical, state)
		d.pdf.Line(dataX, yy, dataX+dataW, yy)
	}

	// Known segments are rendered independently. A segment ends at UNKNOWN,
	// so no line or transition can imply a health trajectory through missing
	// evidence. Within a segment, preserve the client's step-chart semantics.
	for _, segment := range knownHealthSegments(points) {
		d.drawKnownHealthSegment(segment, xForIndex, vertical)
	}
	for _, run := range unknownHealthRuns(points) {
		left, width := unknownChartBounds(run, maxIndex, dataW)
		d.drawUnknownChartLabel(
			dataX+left,
			vertical.goodY,
			width,
			vertical.concernY-vertical.goodY,
		)
	}
	d.renderHealthChartStateLabels(geometry, y, h)
	d.pdf.SetFont("plex", "", 7)
	spansMultipleYears := chartSpansMultipleYears(points)
	for _, index := range healthChartDateIndices(len(points)) {
		px := dataX + dataW*float64(index)/float64(maxInt(1, len(points)-1))
		label := formatChartDate(points[index].Date, d.tr.Locale, spansMultipleYears)
		labelX := chartLabelX(px, dataX, dataW, d.pdf.GetStringWidth(label))
		setTextColor(d.pdf, d.palette.TextSecondary)
		d.pdf.Text(labelX, y+h+healthChartDateGap, label)
	}
	d.renderHealthLegend(y + h + healthChartDateGap + healthChartLegendGap)
	d.pdf.SetY(y + healthChartHeight)
}

func healthChartBounds() (x, width float64) {
	return contentLeft, contentW
}

type healthChartGeometry struct {
	chartLeft      float64
	chartRight     float64
	labelZoneLeft  float64
	labelZoneRight float64
	plotLeft       float64
	plotRight      float64
}

type healthChartVerticalGeometry struct {
	chartTop   float64
	goodY      float64
	watchY     float64
	concernY   float64
	plotBottom float64
}

func (d *document) healthChartGeometry() healthChartGeometry {
	chartLeft, chartWidth := healthChartBounds()
	labelZoneWidth := healthChartLabelZoneWidth(d)
	plotLeft := chartLeft + labelZoneWidth + healthChartAxisGap + healthChartDataInset
	plotRight := chartLeft + chartWidth - healthChartDataInset
	return healthChartGeometry{
		chartLeft:      chartLeft,
		chartRight:     chartLeft + chartWidth,
		labelZoneLeft:  chartLeft,
		labelZoneRight: plotLeft - healthChartAxisGap - healthChartDataInset,
		plotLeft:       plotLeft,
		plotRight:      plotRight,
	}
}

func healthChartVerticalBounds(chartTop, chartHeight float64) healthChartVerticalGeometry {
	return healthChartVerticalGeometry{
		chartTop:   chartTop,
		goodY:      chartY(chartTop, chartHeight, "GOOD"),
		watchY:     chartY(chartTop, chartHeight, "WATCH"),
		concernY:   chartY(chartTop, chartHeight, "CONCERN"),
		plotBottom: chartTop + chartHeight,
	}
}

func healthChartPlotBounds(d *document) (x, width float64) {
	geometry := d.healthChartGeometry()
	return geometry.plotLeft, maxFloat(1, geometry.plotRight-geometry.plotLeft)
}

func healthChartLabelZoneWidth(d *document) float64 {
	d.pdf.SetFont("plex", "", 7)
	maxWidth := 0.0
	for _, state := range knownHealthStates() {
		maxWidth = maxFloat(maxWidth, d.pdf.GetStringWidth(d.tr.T(chartStateLabelKey(state))))
	}
	return maxWidth + healthChartLabelLeadingPadding + healthChartLabelTrailingPadding
}

func (d *document) renderHealthChartStateLabels(geometry healthChartGeometry, y, h float64) {
	vertical := healthChartVerticalBounds(y, h)
	labelBoxX := geometry.labelZoneLeft + healthChartLabelLeadingPadding
	labelBoxWidth := geometry.labelZoneRight - labelBoxX - healthChartLabelTrailingPadding
	for _, state := range knownHealthStates() {
		yy := chartStateY(vertical, state)
		label := d.tr.T(chartStateLabelKey(state))
		d.pdf.SetFont("plex", "", 7)
		labelY := healthChartLabelTopY(yy, 1)
		d.pdf.SetXY(labelBoxX, labelY)
		setTextColor(d.pdf, d.palette.HealthState(state))
		d.pdf.MultiCell(labelBoxWidth, 3.6, label, "", "L", false)
	}
}

func chartStateY(vertical healthChartVerticalGeometry, state string) float64 {
	switch state {
	case "GOOD":
		return vertical.goodY
	case "WATCH":
		return vertical.watchY
	case "CONCERN":
		return vertical.concernY
	default:
		return math.NaN()
	}
}

func healthChartLabelTopY(centerY float64, lineCount int) float64 {
	return centerY - float64(maxInt(1, lineCount))*3.6/2
}

func healthLegendStates() []string { return knownHealthStates() }

func chartStateLabelKey(state string) string {
	switch state {
	case "GOOD":
		return "report.health_history_good"
	case "WATCH":
		return "report.health_history_watch"
	case "CONCERN":
		return "report.health_history_concern"
	default:
		return "report.insufficient_data"
	}
}

func healthChartDateStep(pointCount int) int {
	switch {
	case pointCount <= 14:
		return 2
	case pointCount <= 30:
		return 5
	case pointCount <= 60:
		return 7
	default:
		return 13
	}
}

func healthChartDateIndices(pointCount int) []int {
	if pointCount <= 0 {
		return nil
	}
	last := pointCount - 1
	step := healthChartDateStep(pointCount)
	indices := make([]int, 0, pointCount/step+2)
	for index := 0; index <= last; index += step {
		indices = append(indices, index)
	}
	if indices[len(indices)-1] != last {
		indices = append(indices, last)
	}
	return indices
}

func chartSpansMultipleYears(points []report.HealthHistoryPointData) bool {
	if len(points) < 2 {
		return false
	}
	first, firstErr := time.Parse("2006-01-02", points[0].Date)
	last, lastErr := time.Parse("2006-01-02", points[len(points)-1].Date)
	return firstErr == nil && lastErr == nil && first.Year() != last.Year()
}

func formatChartDate(value, locale string, includeYear bool) string {
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return value
	}
	if includeYear {
		return fmt.Sprintf("%02d.%02d.%02d", date.Day(), int(date.Month()), date.Year()%100)
	}
	return fmt.Sprintf("%02d.%02d", date.Day(), int(date.Month()))
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (d *document) renderHealthLegend(y float64) {
	states := healthLegendStates()
	d.pdf.SetFont("plex", "", 7)
	labelWidths := make([]float64, len(states))
	total := 0.0
	for index, state := range states {
		label := d.tr.T(chartStateLabelKey(state))
		labelWidths[index] = d.pdf.GetStringWidth(label)
		total += healthLegendItemWidth(healthLegendIndicatorWidth, healthLegendIndicatorGap, labelWidths[index])
		if index < len(states)-1 {
			total += healthLegendItemGap
		}
	}
	x := contentLeft + (contentW-total)/2
	indicatorCenterY := y + healthLegendIndicatorCenter
	textBaselineY := pdfTextBaselineForVisualCenter(d.pdf, indicatorCenterY)
	for index, state := range states {
		setDrawColor(d.pdf, d.palette.HealthState(state))
		d.pdf.SetLineWidth(0.8)
		d.pdf.SetLineCapStyle("butt")
		d.pdf.Line(x, indicatorCenterY, x+healthLegendIndicatorWidth, indicatorCenterY)
		x += healthLegendIndicatorWidth + healthLegendIndicatorGap
		d.pdf.SetFont("plex", "", 7)
		setTextColor(d.pdf, d.palette.TextSecondary)
		d.pdf.Text(x, textBaselineY, d.tr.T(chartStateLabelKey(state)))
		x += labelWidths[index]
		if index < len(states)-1 {
			x += healthLegendItemGap
		}
	}
}

func healthLegendItemWidth(indicatorWidth, indicatorGap, labelWidth float64) float64 {
	return indicatorWidth + indicatorGap + labelWidth
}

// fpdf.Text uses a baseline Y coordinate. Convert between that baseline and
// the visual center of the embedded font using its actual ascent/descent
// metrics, rather than treating the nominal font size as glyph height.
func pdfTextVisualCenterY(pdf *fpdf.Fpdf, baselineY float64) float64 {
	_, unitSize := pdf.GetFontSize()
	desc := pdf.GetFontDesc("", "")
	return baselineY - float64(desc.Ascent+desc.Descent)*unitSize/2000
}

func pdfTextBaselineForVisualCenter(pdf *fpdf.Fpdf, centerY float64) float64 {
	_, unitSize := pdf.GetFontSize()
	desc := pdf.GetFontDesc("", "")
	return centerY + float64(desc.Ascent+desc.Descent)*unitSize/2000
}

type healthChartRun struct {
	start int
	end   int
}

func knownHealthStates() []string { return []string{"GOOD", "WATCH", "CONCERN"} }

func isKnownHealthState(state string) bool {
	return state == "GOOD" || state == "WATCH" || state == "CONCERN"
}

func unknownHealthRuns(points []report.HealthHistoryPointData) []healthChartRun {
	runs := []healthChartRun{}
	start := -1
	for index, point := range points {
		if point.State == "UNKNOWN" {
			if start < 0 {
				start = index
			}
			continue
		}
		if start >= 0 {
			runs = append(runs, healthChartRun{start: start, end: index - 1})
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, healthChartRun{start: start, end: len(points) - 1})
	}
	return runs
}

func unknownChartBounds(run healthChartRun, maxIndex int, chartWidth float64) (left, width float64) {
	if maxIndex <= 0 {
		return 0, chartWidth
	}
	slotWidth := chartWidth / float64(maxIndex)
	startCenter := float64(run.start) / float64(maxIndex) * chartWidth
	endCenter := float64(run.end) / float64(maxIndex) * chartWidth
	left = maxFloat(0, startCenter-slotWidth/2)
	right := minFloat(chartWidth, endCenter+slotWidth/2)
	return left, maxFloat(0, right-left)
}

type healthChartSegment struct {
	start  int
	points []report.HealthHistoryPointData
}

func knownHealthSegments(points []report.HealthHistoryPointData) []healthChartSegment {
	segments := []healthChartSegment{}
	start := -1
	for index, point := range points {
		if !isKnownHealthState(point.State) {
			if start >= 0 {
				segments = append(segments, healthChartSegment{start: start, points: append([]report.HealthHistoryPointData(nil), points[start:index]...)})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = index
		}
	}
	if start >= 0 {
		segments = append(segments, healthChartSegment{start: start, points: append([]report.HealthHistoryPointData(nil), points[start:]...)})
	}
	return segments
}

func (d *document) drawKnownHealthSegment(segment healthChartSegment, xForIndex func(int) float64, vertical healthChartVerticalGeometry) {
	runStart := 0
	for index := 1; index <= len(segment.points); index++ {
		if index < len(segment.points) && segment.points[index].State == segment.points[runStart].State {
			continue
		}
		state := segment.points[runStart].State
		startX := xForIndex(segment.start + runStart)
		endX := xForIndex(segment.start + index - 1)
		if index < len(segment.points) {
			endX = xForIndex(segment.start + index)
		}
		setDrawColor(d.pdf, d.palette.HealthState(state))
		d.pdf.SetLineWidth(1.1)
		if endX > startX {
			d.pdf.SetLineCapStyle("butt")
			d.pdf.Line(startX, chartStateY(vertical, state), endX, chartStateY(vertical, state))
		}
		if index < len(segment.points) {
			nextState := segment.points[index].State
			setDrawColor(d.pdf, d.palette.Border)
			d.pdf.SetLineWidth(0.8)
			x := xForIndex(segment.start + index)
			d.pdf.Line(x, chartStateY(vertical, state), x, chartStateY(vertical, nextState))
		}
		d.pdf.SetLineCapStyle("butt")
		runStart = index
	}
}

func (d *document) drawUnknownChartLabel(x, y, width, height float64) {
	const horizontalPadding = 2.5
	availableWidth := width - 2*horizontalPadding
	if availableWidth <= 0 {
		return
	}
	d.pdf.SetFont("plex", "", 7)
	labelLines := d.splitText(d.tr.T("report.insufficient_data"), availableWidth, "plex", "", 7)
	if len(labelLines) > 2 {
		return
	}
	labelHeight := float64(len(labelLines)) * 4
	labelX := x + horizontalPadding
	labelY := y + (height-labelHeight)/2
	d.pdf.SetXY(labelX, labelY)
	setTextColor(d.pdf, d.palette.TextSecondary)
	d.pdf.MultiCell(availableWidth, 4, strings.Join(labelLines, "\n"), "", "C", false)
}

// chartLabelX centers a date label on its timeline point while keeping its
// complete bounding box inside the chart's safe content bounds.
func chartLabelX(pointX, chartX, chartW, labelW float64) float64 {
	if labelW >= chartW {
		return chartX
	}
	left := pointX - labelW/2
	minX := chartX
	maxX := chartX + chartW - labelW
	if left < minX {
		return minX
	}
	if left > maxX {
		return maxX
	}
	return left
}

func chartY(y, h float64, state string) float64 {
	rank, ok := map[string]float64{"CONCERN": 0, "WATCH": 1, "GOOD": 2}[state]
	if !ok {
		return math.NaN()
	}
	usableHeight := h - 2*healthChartPlotInset
	return y + healthChartPlotInset + usableHeight - rank*usableHeight/2
}

func (d *document) inspections(model *report.HiveReport) error {
	widths := inspectionTableWidths()
	headers := []string{d.tr.T("report.inspection_date"), d.tr.T("report.type"), d.tr.T("report.assessment")}
	minimumContentHeight := d.measureEmptyState(d.tr.T("report.no_inspections"))
	if len(model.Inspections) > 0 {
		minimumContentHeight = d.measureTableStart(headers, widths, d.inspectionRowValues(model.Inspections[0]), rowLineH)
	}
	if err := d.section(d.tr.T("report.inspections"), minimumContentHeight); err != nil {
		return err
	}
	if len(model.Inspections) == 0 {
		d.empty(d.tr.T("report.no_inspections"))
		return nil
	}
	d.tableHeader(headers, widths)
	for _, item := range model.Inspections {
		d.row(d.inspectionRowValues(item), widths, rowLineH)
	}
	return nil
}

func (d *document) inspectionRowValues(item report.InspectionData) []string {
	return []string{formatDate(item.InspectedAt, d.tr.Locale), d.tr.Enum(item.Type), d.assessment(item.Assessment)}
}

func (d *document) assessment(value *report.AssessmentData) string {
	if value == nil {
		return d.tr.T("report.no_assessment")
	}
	rows := []string{}
	add := func(key string, v *string) {
		if v != nil {
			rows = append(rows, d.tr.Label(key)+": "+d.tr.Enum(*v))
		}
	}
	add("colonyStrength", value.ColonyStrength)
	add("queenStatus", value.QueenStatus)
	add("broodStatus", value.BroodStatus)
	add("foodStores", value.FoodStores)
	add("healthConcerns", value.HealthConcerns)
	add("queenObserved", value.QueenObserved)
	add("eggsObserved", value.EggsObserved)
	add("queenCells", value.QueenCells)
	add("queenCondition", value.QueenCondition)
	add("broodAmount", value.BroodAmount)
	add("broodPattern", value.BroodPattern)
	add("broodConcerns", value.BroodConcerns)
	add("healthOverallCondition", value.HealthOverallCondition)
	add("healthConcernLevel", value.HealthConcernLevel)
	add("feedingNeed", value.FeedingNeed)
	add("feedingPerformed", value.FeedingPerformed)
	add("season", value.Season)
	add("seasonalStoreReadiness", value.SeasonalStoreReadiness)
	add("seasonalReadiness", value.SeasonalReadiness)
	addList := func(key string, values *[]string) {
		if values != nil && len(*values) > 0 {
			translated := make([]string, len(*values))
			for i, item := range *values {
				translated[i] = d.tr.Enum(item)
			}
			rows = append(rows, d.tr.Label(key)+": "+strings.Join(translated, ", "))
		}
	}
	addList("broodStages", value.BroodStages)
	addList("pestSigns", value.PestSigns)
	addList("healthWarningSigns", value.HealthWarningSigns)
	addList("feedTypes", value.FeedTypes)
	addList("seasonalConcerns", value.SeasonalConcerns)
	if len(rows) == 0 {
		return d.tr.T("report.no_assessment")
	}
	return strings.Join(rows, "; ")
}

func (d *document) queens(model *report.HiveReport) error {
	widths := d.queenTableWidths()
	headers := []string{d.tr.T("report.introduced"), d.tr.T("report.removed"), d.tr.T("report.year_color"), d.tr.T("report.current_queen"), d.tr.T("report.replacement_reason")}
	minimumContentHeight := d.measureEmptyState(d.tr.T("report.no_queens"))
	if len(model.Queens) > 0 {
		minimumContentHeight = d.measureTableStart(headers, widths, d.queenRowValues(model.Queens[0]), rowLineH)
	}
	if err := d.section(d.tr.T("report.queen_history"), minimumContentHeight); err != nil {
		return err
	}
	if len(model.Queens) == 0 {
		d.empty(d.tr.T("report.no_queens"))
		return nil
	}
	d.tableHeader(headers, widths)
	for _, queen := range model.Queens {
		d.row(d.queenRowValues(queen), widths, rowLineH)
	}
	return nil
}

func (d *document) queenRowValues(queen report.QueenData) []string {
	removed := ""
	if queen.RemovedAt != nil {
		removed = formatTimeDate(*queen.RemovedAt, d.tr.Locale)
	}
	current := ""
	if queen.Current {
		current = d.tr.T("report.current_queen")
	}
	reason := ""
	if queen.ReplacementReason != nil {
		reason = d.tr.Enum(*queen.ReplacementReason)
	}
	color := d.tr.Enum(strings.ToUpper(queen.MarkingColor))
	return []string{formatTimeDate(queen.IntroducedAt, d.tr.Locale), removed, fmt.Sprintf("%d / %s", queen.Year, color), current, reason}
}

func (d *document) harvests(model *report.HiveReport) error {
	widths := harvestTableWidths()
	headers := []string{d.tr.T("report.date"), d.tr.T("report.product"), d.tr.T("report.amount"), d.tr.T("report.unit")}
	minimumContentHeight := d.measureEmptyState(d.tr.T("report.no_harvests"))
	if len(model.Harvests) > 0 {
		minimumContentHeight = d.measureTableStart(headers, widths, d.harvestRowValues(model.Harvests[0]), rowLineH)
	}
	if err := d.section(d.tr.T("report.harvests"), minimumContentHeight); err != nil {
		return err
	}
	if len(model.Harvests) == 0 {
		d.empty(d.tr.T("report.no_harvests"))
		return nil
	}
	d.tableHeader(headers, widths)
	for _, item := range model.Harvests {
		d.row(d.harvestRowValues(item), widths, rowLineH)
	}
	d.endTable()
	if len(model.HarvestTotals) == 0 {
		return nil
	}
	totalWidths := harvestTotalWidths()
	firstTotal := d.harvestTotalRowValues(model.HarvestTotals[0])
	d.setBody()
	_, firstTotalHeight := d.measureWrappedRow(firstTotal, totalWidths, rowLineH, tablePaddingY, tableMinRowHeight)
	d.ensureBlockStartFits(totalBlockGap+totalLabelHeight, 0, firstTotalHeight)
	d.pdf.Ln(totalBlockGap)
	d.pdf.SetFont("plex", "B", 10)
	d.pdf.CellFormat(contentW, totalLabelHeight, d.tr.T("report.total"), "", 1, "L", false, 0, "")
	d.setBody()
	for _, total := range model.HarvestTotals {
		d.row(d.harvestTotalRowValues(total), totalWidths, rowLineH)
	}
	return nil
}

func (d *document) harvestRowValues(item report.HarvestData) []string {
	return []string{formatDate(item.HarvestedAt, d.tr.Locale), d.tr.Enum(item.Product), fmt.Sprintf("%.2f", item.Amount), d.tr.Enum(item.Unit)}
}

func (d *document) harvestTotalRowValues(total report.HarvestTotal) []string {
	return []string{d.tr.Enum(total.Product), fmt.Sprintf("%.2f", total.Amount), d.tr.Enum(total.Unit)}
}

func (d *document) summary(model *report.HiveReport) error {
	if err := d.section(d.tr.T("report.summary"), 3*tableMinRowHeight); err != nil {
		return err
	}
	d.summaryRow(d.tr.T("report.inspections"), strconv.Itoa(len(model.Inspections)))
	d.summaryRow(d.tr.T("report.queen_history"), strconv.Itoa(len(model.Queens)))
	d.summaryRow(d.tr.T("report.harvests"), strconv.Itoa(len(model.Harvests)))
	return nil
}

func (d *document) summaryRow(label, value string) {
	const labelW = 100.0
	d.wrappedRow([]string{label, value}, []float64{labelW, contentW - labelW}, rowLineH, tablePaddingY, tableMinRowHeight, false, true)
}

func (d *document) tableHeader(values []string, widths []float64) {
	setTextColor(d.pdf, d.palette.TextPrimary)
	d.pdf.SetFont("plex", "B", 8)
	headerLines, headerHeight := d.measureWrappedRow(values, widths, 7, tableHeaderPaddingY, tableMinRowHeight)
	d.table = &tableState{
		headers:      append([]string(nil), values...),
		headerLines:  headerLines,
		widths:       append([]float64(nil), widths...),
		lineHeight:   7,
		verticalPad:  tableHeaderPaddingY,
		minRowHeight: tableMinRowHeight,
		headerHeight: headerHeight,
	}
	d.setBody()
}

func (d *document) row(values []string, widths []float64, height float64) {
	if d.table != nil {
		d.tableRow(values, widths, height)
		return
	}
	d.wrappedRow(values, widths, height, tablePaddingY, tableMinRowHeight, false, true)
}

func (d *document) endTable() {
	d.table = nil
}

func (d *document) tableRow(values []string, widths []float64, lineHeight float64) {
	lines, rowHeight := d.measureWrappedRow(values, widths, lineHeight, tablePaddingY, tableMinRowHeight)
	state := d.table
	if state.bodyRows == 0 {
		d.ensureBlockStartFits(state.headerHeight, 0, rowHeight)
		d.drawWrappedRow(state.headerLines, state.widths, state.lineHeight, state.verticalPad, state.minRowHeight, true, false, state.headerHeight)
		d.drawWrappedRow(lines, widths, lineHeight, tablePaddingY, tableMinRowHeight, false, true, rowHeight)
	} else {
		if !tableRowFits(d.pdf.GetY(), rowHeight) {
			d.pdf.AddPage()
			// The repeated header is an introducer for the continuation row.
			d.ensureBlockStartFits(state.headerHeight, 0, rowHeight)
			d.drawWrappedRow(state.headerLines, state.widths, state.lineHeight, state.verticalPad, state.minRowHeight, true, false, state.headerHeight)
		}
		d.drawWrappedRow(lines, widths, lineHeight, tablePaddingY, tableMinRowHeight, false, true, rowHeight)
	}
	state.bodyRows++
}

func tableStartFits(currentY, headerHeight, firstRowHeight float64) bool {
	return blockStartFits(currentY, headerHeight, 0, firstRowHeight)
}

func sectionStartFits(currentY, minimumContentHeight float64, hasPreviousSection bool) bool {
	gap := 0.0
	if hasPreviousSection {
		gap = sectionGapBefore
	}
	return blockStartFits(currentY, gap+sectionTitleHeight, sectionContentGap, minimumContentHeight)
}

func tableRowFits(currentY, rowHeight float64) bool {
	return blockStartFits(currentY, 0, 0, rowHeight)
}

// blockStartFits reports whether an introducer and its minimum following content
// fit in the canonical printable area. The >= boundary is intentionally treated
// as fitting so exact page boundaries do not create avoidable blank space.
func blockStartFits(currentY, introducerHeight, gapAfterIntroducer, followingHeight float64) bool {
	return currentY+introducerHeight+gapAfterIntroducer+followingHeight <= contentBottom+fitEpsilon
}

// ensureBlockStartFits applies the keep-with-next rule. It moves the block to a
// fresh page only when the block does not fit and the current page is not already
// fresh. An oversized child is rendered on the fresh page without retrying, so
// pagination cannot loop or create repeated blank pages.
func (d *document) ensureBlockStartFits(introducerHeight, gapAfterIntroducer, followingHeight float64) bool {
	if blockStartFits(d.pdf.GetY(), introducerHeight, gapAfterIntroducer, followingHeight) {
		return false
	}
	if d.pdf.GetY() > margin+fitEpsilon {
		d.pdf.AddPage()
		return true
	}
	return false
}

func (d *document) wrappedRow(values []string, widths []float64, lineHeight, verticalPadding, minHeight float64, header, separator bool) {
	lines, rowHeight := d.measureWrappedRow(values, widths, lineHeight, verticalPadding, minHeight)
	d.ensureBlockStartFits(0, 0, rowHeight)
	d.drawWrappedRow(lines, widths, lineHeight, verticalPadding, minHeight, header, separator, rowHeight)
}

func (d *document) measureWrappedRow(values []string, widths []float64, lineHeight, verticalPadding, minHeight float64) ([][]string, float64) {
	lines := make([][]string, len(values))
	rowHeight := minHeight
	for i, value := range values {
		lines[i] = d.wrapCell(value, widths[i])
		if height := float64(len(lines[i]))*lineHeight + 2*verticalPadding; height > rowHeight {
			rowHeight = height
		}
	}
	return lines, rowHeight
}

func (d *document) measureTableStart(headers []string, widths []float64, firstRow []string, lineHeight float64) float64 {
	d.pdf.SetFont("plex", "B", 8)
	_, headerHeight := d.measureWrappedRow(headers, widths, 7, tableHeaderPaddingY, tableMinRowHeight)
	d.setBody()
	_, firstRowHeight := d.measureWrappedRow(firstRow, widths, lineHeight, tablePaddingY, tableMinRowHeight)
	return headerHeight + firstRowHeight
}

func (d *document) measureEmptyState(value string) float64 {
	d.setBody()
	lines := d.pdf.SplitLines([]byte(value), contentW)
	return maxFloat(6, float64(len(lines))*6)
}

func (d *document) drawWrappedRow(lines [][]string, widths []float64, lineHeight, verticalPadding, minHeight float64, header, separator bool, rowHeight float64) {
	if header {
		setTextColor(d.pdf, d.palette.TextPrimary)
		d.pdf.SetFont("plex", "B", 8)
	} else {
		d.setBody()
	}
	y := d.pdf.GetY()
	x := contentLeft
	for i, cellLines := range lines {
		width := widths[i]
		surface := reportTableStyle.bodyBackground
		if header {
			surface = reportTableStyle.headerBackground
		}
		setFillColor(d.pdf, surface)
		d.pdf.Rect(x, y, width, rowHeight, "F")
		contentHeight := float64(len(cellLines)) * lineHeight
		textY := y + verticalPadding + (rowHeight-2*verticalPadding-contentHeight)/2
		d.pdf.SetXY(x+tablePaddingX, textY)
		innerWidth := maxFloat(1, width-2*tablePaddingX)
		d.pdf.MultiCell(innerWidth, lineHeight, strings.Join(cellLines, "\n"), "", "L", false)
		x += width
	}
	d.tableColumnSeparators(widths, y, y+rowHeight)
	if separator {
		setDrawColor(d.pdf, reportTableStyle.border)
		d.pdf.SetLineWidth(reportTableStyle.borderWidth)
		d.pdf.Line(contentLeft, y+rowHeight, contentRight, y+rowHeight)
	}
	d.pdf.SetXY(contentLeft, y+rowHeight)
}

func (d *document) tableColumnSeparators(widths []float64, top, bottom float64) {
	if len(widths) < 2 {
		return
	}
	setDrawColor(d.pdf, reportTableStyle.border)
	d.pdf.SetLineWidth(reportTableStyle.borderWidth)
	for _, x := range tableColumnBoundaries(widths) {
		d.pdf.Line(x, top, x, bottom)
	}
}

func tableColumnBoundaries(widths []float64) []float64 {
	if len(widths) < 2 {
		return nil
	}
	boundaries := make([]float64, 0, len(widths)-1)
	x := contentLeft
	for _, width := range widths[:len(widths)-1] {
		x += width
		boundaries = append(boundaries, x)
	}
	return boundaries
}

func (d *document) wrapCell(value string, width float64) []string {
	value = displayCellValue(value)
	innerWidth := maxFloat(1, width-2*tablePaddingX)
	lines := d.pdf.SplitText(value, innerWidth)
	if len(lines) == 0 {
		return []string{""}
	}
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = line
	}
	return result
}

const emptyCellPlaceholder = "--"

func displayCellValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return emptyCellPlaceholder
	}
	return value
}

func (d *document) empty(value string) {
	setTextColor(d.pdf, d.palette.TextSecondary)
	d.pdf.SetFont("plex", "", 9)
	d.opticalMultiCell(contentW, 6, value, "")
	d.setBody()
}

// opticalCell and opticalMultiCell compensate only for the actual left ink
// bearing of the first glyph. They are limited to text whose logical left edge
// is the report content boundary; table and chart text keeps its internal
// padding and geometry.
func (d *document) opticalCell(width, height float64, text, style string, ln int) {
	x, y := d.pdf.GetXY()
	offset := d.inkOffset(text, style)
	d.pdf.SetXY(x-offset, y)
	d.pdf.CellFormat(width+offset, height, text, "", ln, "L", false, 0, "")
	if ln == 0 {
		d.pdf.SetXY(x+width, y)
	} else {
		d.pdf.SetX(x)
	}
}

func (d *document) opticalMultiCell(width, height float64, text, style string) {
	x, y := d.pdf.GetXY()
	offset := d.inkOffset(text, style)
	d.pdf.SetXY(x-offset, y)
	d.pdf.MultiCell(width+offset, height, text, "", "L", false)
	d.pdf.SetX(x)
}

func (d *document) inkOffset(text, style string) float64 {
	_, unitSize := d.pdf.GetFontSize()
	if style == "B" {
		return d.boldMetric.leftInkOffset(text, unitSize)
	}
	return d.regularMetric.leftInkOffset(text, unitSize)
}

func setTextColor(pdf *fpdf.Fpdf, color RGB) { pdf.SetTextColor(color.R, color.G, color.B) }
func setDrawColor(pdf *fpdf.Fpdf, color RGB) { pdf.SetDrawColor(color.R, color.G, color.B) }
func setFillColor(pdf *fpdf.Fpdf, color RGB) { pdf.SetFillColor(color.R, color.G, color.B) }

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func inspectionTableWidths() []float64 { return []float64{32, 35, contentW - 32 - 35} }

func healthTableWidths() []float64 { return []float64{43, 35, 35, contentW - 43 - 35 - 35} }

const queenIntroducedColumnFloor = 30.0

func queenTableWidths() []float64 {
	return []float64{queenIntroducedColumnFloor, 28, 35, 35, contentW - queenIntroducedColumnFloor - 28 - 35 - 35}
}

func (d *document) queenTableWidths() []float64 {
	d.pdf.SetFont("plex", "B", 8)
	introducedWidth := queenIntroducedColumnFloor
	for _, locale := range []string{"en", "uk"} {
		catalog, err := NewCatalog(locale)
		if err != nil {
			continue
		}
		required := d.pdf.GetStringWidth(catalog.T("report.introduced")) + 2*tablePaddingX
		introducedWidth = maxFloat(introducedWidth, required)
	}
	return []float64{introducedWidth, 28, 35, 35, contentW - introducedWidth - 28 - 35 - 35}
}

func harvestTableWidths() []float64 { return []float64{40, contentW - 40 - 35 - 30, 35, 30} }

func harvestTotalWidths() []float64 { return []float64{contentW - 45 - 45, 45, 45} }

func (c Catalog) Label(field string) string {
	if value, ok := c.Text["assessment."+field]; ok {
		return value
	}
	return humanize(field)
}

func formatDate(value, locale string) string {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		if len(value) >= 10 {
			return value[:10]
		}
		return value
	}
	return formatTimeDate(parsed, locale)
}

func formatTimeDate(value time.Time, locale string) string {
	monthsEN := [...]string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	monthsUK := [...]string{"", "січ", "лют", "бер", "квіт", "трав", "черв", "лип", "серп", "вер", "жовт", "лист", "груд"}
	months := monthsEN[:]
	if locale == "uk" {
		months = monthsUK[:]
	}
	return fmt.Sprintf("%02d %s %04d", value.Day(), months[value.Month()], value.Year())
}

func formatTime(value time.Time, locale string) string {
	return formatTimeDate(value, locale) + " " + value.UTC().Format("15:04") + " UTC"
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
