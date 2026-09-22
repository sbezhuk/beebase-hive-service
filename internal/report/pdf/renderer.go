package pdf

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
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
	pageWidth  = 210.0
	pageHeight = 297.0
	margin     = 15.0
	contentW   = pageWidth - 2*margin
)

type Renderer struct {
	regular []byte
	bold    []byte
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
	return &Renderer{regular: regular, bold: bold}, nil
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
		pdf.SetTextColor(120, 130, 140)
		pdf.CellFormat(contentW, 5, strconv.Itoa(pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()
	doc := &document{pdf: pdf, tr: tr, ctx: ctx}
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
	pdf *fpdf.Fpdf
	tr  Catalog
	ctx context.Context
}

func (d *document) check() error {
	if err := d.ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrRender, err)
	}
	return nil
}

func (d *document) setBody() {
	d.pdf.SetFont("plex", "", 9)
	d.pdf.SetTextColor(45, 55, 65)
}

func (d *document) header(model *report.HiveReport) error {
	if err := d.check(); err != nil {
		return err
	}
	d.pdf.SetTextColor(35, 55, 72)
	d.pdf.SetFont("plex", "B", 23)
	d.pdf.CellFormat(130, 10, d.tr.T("report.title"), "", 1, "L", false, 0, "")
	d.pdf.SetFont("plex", "B", 14)
	d.pdf.SetTextColor(60, 75, 86)
	d.pdf.CellFormat(130, 8, model.Hive.Name, "", 1, "L", false, 0, "")
	d.pdf.SetDrawColor(205, 215, 220)
	d.pdf.Line(margin, d.pdf.GetY()+2, pageWidth-margin, d.pdf.GetY()+2)
	d.pdf.SetY(d.pdf.GetY() + 8)
	d.setBody()
	d.pdf.CellFormat(37, 6, d.tr.T("report.period"), "", 0, "L", false, 0, "")
	d.pdf.CellFormat(70, 6, formatDate(model.Metadata.From.String(), d.tr.Locale)+" - "+formatDate(model.Metadata.To.String(), d.tr.Locale), "", 1, "L", false, 0, "")
	d.pdf.CellFormat(37, 6, d.tr.T("report.generated_at"), "", 0, "L", false, 0, "")
	d.pdf.CellFormat(70, 6, formatTime(model.Metadata.GeneratedAt, d.tr.Locale), "", 1, "L", false, 0, "")
	d.pdf.CellFormat(37, 6, d.tr.T("report.hive_id"), "", 0, "L", false, 0, "")
	d.pdf.CellFormat(100, 6, model.Hive.ID.String(), "", 1, "L", false, 0, "")
	d.pdf.CellFormat(37, 6, d.tr.T("report.apiary_id"), "", 0, "L", false, 0, "")
	d.pdf.CellFormat(100, 6, model.Hive.ApiaryID.String(), "", 1, "L", false, 0, "")
	if model.Hive.Notes != "" {
		d.pdf.CellFormat(37, 6, "", "", 0, "L", false, 0, "")
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
	x := pageWidth - margin - 34
	y := margin + 5
	d.pdf.ImageOptions("hive-qr", x, y, 34, 34, false, fpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, 0, "")
	d.pdf.SetXY(x-1, y+35)
	d.pdf.SetFont("plex", "", 7)
	d.pdf.SetTextColor(80, 90, 98)
	d.pdf.MultiCell(36, 3, d.tr.T("report.qr_instruction"), "", "C", false)
	d.pdf.SetY(contentY)
	d.setBody()
	return nil
}

func (d *document) section(title string) error {
	if err := d.check(); err != nil {
		return err
	}
	if d.pdf.GetY() > pageHeight-42 {
		d.pdf.AddPage()
	}
	d.pdf.SetTextColor(35, 55, 72)
	d.pdf.SetFont("plex", "B", 15)
	d.pdf.CellFormat(contentW, 8, title, "", 1, "L", false, 0, "")
	d.pdf.SetDrawColor(110, 160, 165)
	d.pdf.SetLineWidth(0.7)
	d.pdf.Line(margin, d.pdf.GetY(), pageWidth-margin, d.pdf.GetY())
	d.pdf.Ln(4)
	d.setBody()
	return nil
}

func (d *document) health(model *report.HiveReport) error {
	if err := d.section(d.tr.T("report.colony_health")); err != nil {
		return err
	}
	d.pdf.SetFont("plex", "B", 10)
	d.pdf.CellFormat(35, 6, d.tr.T("report.state"), "", 0, "L", false, 0, "")
	d.setBody()
	d.pdf.CellFormat(45, 6, d.tr.Enum(model.Health.State), "", 0, "L", false, 0, "")
	d.pdf.SetFont("plex", "B", 10)
	d.pdf.CellFormat(35, 6, d.tr.T("report.coverage"), "", 0, "L", false, 0, "")
	d.setBody()
	d.pdf.CellFormat(45, 6, d.tr.Enum(model.Health.Coverage), "", 1, "L", false, 0, "")
	d.tableHeader([]string{d.tr.T("report.health_dimension"), d.tr.T("report.state"), d.tr.T("report.coverage"), d.tr.T("report.health_evidence")}, []float64{43, 35, 35, 67})
	for _, dimension := range model.Health.Dimensions {
		evidence := make([]string, 0, len(dimension.Sources))
		for _, source := range dimension.Sources {
			evidence = append(evidence, formatDate(source.InspectedAt, d.tr.Locale))
		}
		d.row([]string{d.tr.Enum(dimension.Dimension), d.tr.Enum(dimension.State), d.tr.Enum(dimension.Coverage), strings.Join(evidence, ", ")}, []float64{43, 35, 35, 67}, 5)
	}
	d.pdf.Ln(4)
	return nil
}

func (d *document) history(model *report.HiveReport) error {
	if err := d.section(d.tr.T("report.health_history")); err != nil {
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
	x, y, w, h := margin, d.pdf.GetY(), contentW, 54.0
	d.pdf.SetDrawColor(220, 226, 230)
	d.pdf.SetLineWidth(0.25)
	for i := 0; i <= 3; i++ {
		yy := y + h - float64(i)*h/3
		d.pdf.Line(x, yy, x+w, yy)
	}
	d.pdf.SetFont("plex", "", 7)
	d.pdf.SetTextColor(100, 110, 118)
	for i, state := range []string{"CONCERN", "WATCH", "GOOD", "UNKNOWN"} {
		yy := y + h - float64(i)*h/3 - 1
		d.pdf.Text(x, yy, d.tr.Enum(state))
	}
	if len(points) == 1 {
		px := x + w/2
		py := chartY(y, h, points[0].State)
		d.pdf.SetFillColor(58, 126, 131)
		d.pdf.Circle(px, py, 1.7, "F")
	} else {
		for i := 1; i < len(points); i++ {
			px1 := x + w*float64(i-1)/float64(len(points)-1)
			px2 := x + w*float64(i)/float64(len(points)-1)
			d.pdf.SetDrawColor(58, 126, 131)
			d.pdf.SetLineWidth(1.1)
			d.pdf.Line(px1, chartY(y, h, points[i-1].State), px2, chartY(y, h, points[i].State))
		}
	}
	d.pdf.SetFont("plex", "", 7)
	for _, index := range []int{0, len(points) - 1} {
		px := x + w*float64(index)/float64(maxInt(1, len(points)-1))
		d.pdf.SetTextColor(100, 110, 118)
		d.pdf.Text(px, y+h+5, formatDate(points[index].Date, d.tr.Locale))
	}
	d.pdf.SetY(y + h + 9)
}

func chartY(y, h float64, state string) float64 {
	rank := map[string]float64{"CONCERN": 0, "WATCH": 1, "GOOD": 2, "UNKNOWN": 3}[state]
	return y + h - rank*h/3
}

func (d *document) inspections(model *report.HiveReport) error {
	if err := d.section(d.tr.T("report.inspections")); err != nil {
		return err
	}
	if len(model.Inspections) == 0 {
		d.empty(d.tr.T("report.no_inspections"))
		return nil
	}
	d.tableHeader([]string{d.tr.T("report.inspection_date"), d.tr.T("report.type"), d.tr.T("report.assessment")}, []float64{32, 35, 103})
	for _, item := range model.Inspections {
		assessment := d.assessment(item.Assessment)
		d.row([]string{formatDate(item.InspectedAt, d.tr.Locale), d.tr.Enum(item.Type), assessment}, []float64{32, 35, 103}, 5)
	}
	return nil
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
	if err := d.section(d.tr.T("report.queen_history")); err != nil {
		return err
	}
	if len(model.Queens) == 0 {
		d.empty(d.tr.T("report.no_queens"))
		return nil
	}
	d.tableHeader([]string{d.tr.T("report.introduced"), d.tr.T("report.removed"), d.tr.T("report.year_color"), d.tr.T("report.current_queen"), d.tr.T("report.replacement_reason")}, []float64{28, 28, 35, 35, 44})
	for _, queen := range model.Queens {
		removed := d.tr.T("report.no_replacement")
		if queen.RemovedAt != nil {
			removed = formatTimeDate(*queen.RemovedAt, d.tr.Locale)
		}
		current := ""
		if queen.Current {
			current = d.tr.T("report.current_queen")
		}
		reason := d.tr.T("report.no_replacement")
		if queen.ReplacementReason != nil {
			reason = d.tr.Enum(*queen.ReplacementReason)
		}
		color := d.tr.Enum(strings.ToUpper(queen.MarkingColor))
		d.row([]string{formatTimeDate(queen.IntroducedAt, d.tr.Locale), removed, fmt.Sprintf("%d / %s", queen.Year, color), current, reason}, []float64{28, 28, 35, 35, 44}, 5)
	}
	return nil
}

func (d *document) harvests(model *report.HiveReport) error {
	if err := d.section(d.tr.T("report.harvests")); err != nil {
		return err
	}
	if len(model.Harvests) == 0 {
		d.empty(d.tr.T("report.no_harvests"))
		return nil
	}
	d.tableHeader([]string{d.tr.T("report.date"), d.tr.T("report.product"), d.tr.T("report.amount"), d.tr.T("report.unit")}, []float64{40, 65, 35, 30})
	for _, item := range model.Harvests {
		d.row([]string{formatDate(item.HarvestedAt, d.tr.Locale), d.tr.Enum(item.Product), fmt.Sprintf("%.2f", item.Amount), d.tr.Enum(item.Unit)}, []float64{40, 65, 35, 30}, 5)
	}
	d.pdf.Ln(2)
	d.pdf.SetFont("plex", "B", 10)
	d.pdf.CellFormat(contentW, 6, d.tr.T("report.total"), "", 1, "L", false, 0, "")
	d.setBody()
	for _, total := range model.HarvestTotals {
		d.row([]string{d.tr.Enum(total.Product), fmt.Sprintf("%.2f", total.Amount), d.tr.Enum(total.Unit)}, []float64{80, 45, 45}, 5)
	}
	return nil
}

func (d *document) summary(model *report.HiveReport) error {
	if err := d.section(d.tr.T("report.summary")); err != nil {
		return err
	}
	d.row([]string{d.tr.T("report.inspections"), strconv.Itoa(len(model.Inspections))}, []float64{100, 70}, 6)
	d.row([]string{d.tr.T("report.queen_history"), strconv.Itoa(len(model.Queens))}, []float64{100, 70}, 6)
	d.row([]string{d.tr.T("report.harvests"), strconv.Itoa(len(model.Harvests))}, []float64{100, 70}, 6)
	return nil
}

func (d *document) tableHeader(values []string, widths []float64) {
	d.pdf.SetFillColor(239, 244, 245)
	d.pdf.SetTextColor(45, 65, 75)
	d.pdf.SetFont("plex", "B", 8)
	for i, value := range values {
		d.pdf.CellFormat(widths[i], 7, value, "", 0, "L", true, 0, "")
	}
	d.pdf.Ln(-1)
	d.setBody()
}

func (d *document) row(values []string, widths []float64, height float64) {
	if d.pdf.GetY()+height > pageHeight-18 {
		d.pdf.AddPage()
	}
	for i, value := range values {
		d.pdf.CellFormat(widths[i], height, value, "B", 0, "L", false, 0, "")
	}
	d.pdf.Ln(-1)
}

func (d *document) empty(value string) {
	d.pdf.SetTextColor(105, 115, 122)
	d.pdf.SetFont("plex", "", 9)
	d.pdf.MultiCell(contentW, 6, value, "", "L", false)
	d.setBody()
}

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
