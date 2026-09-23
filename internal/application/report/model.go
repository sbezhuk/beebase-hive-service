package report

import (
	"time"

	"github.com/google/uuid"
)

// HiveReport is the complete, presentation-neutral report input assembled by
// hive-service. The PDF renderer must be able to consume this model without
// querying a repository or another service.
type HiveReport struct {
	Metadata      ReportMetadata
	Hive          HiveData
	Queens        []QueenData
	Inspections   []InspectionData
	Health        HealthData
	HealthHistory HealthHistoryData
	Harvests      []HarvestData
	HarvestTotals []HarvestTotal
}

type ReportMetadata struct {
	From        time.Time
	To          time.Time
	Locale      string
	GeneratedAt time.Time
}

type HiveData struct {
	ID         uuid.UUID
	ApiaryID   uuid.UUID
	ApiaryName *string
	Name       string
	Notes      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ApiaryDisplayInfo struct {
	ID   uuid.UUID
	Name string
}

type QueenData struct {
	ID                uuid.UUID
	MarkedAt          time.Time
	IntroducedAt      time.Time
	RemovedAt         *time.Time
	Year              int
	MarkingColor      string
	MarkingColorHex   string
	ReplacementReason *string
	Notes             string
	Current           bool
}

// InspectionData and Assessment intentionally use typed scalar fields rather
// than exposing the inspection service's persistence or raw JSON models.
type InspectionData struct {
	ID          uuid.UUID
	HiveID      uuid.UUID
	InspectedAt string
	Notes       string
	Type        string
	TypeLabel   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Assessment  *AssessmentData
}

type AssessmentData struct {
	Version                int
	ColonyStrength         *string
	QueenStatus            *string
	BroodStatus            *string
	FoodStores             *string
	HealthConcerns         *string
	QueenObserved          *string
	EggsObserved           *string
	QueenCells             *string
	QueenCondition         *string
	BroodAmount            *string
	BroodPattern           *string
	BroodStages            *[]string
	BroodConcerns          *string
	HealthOverallCondition *string
	PestSigns              *[]string
	HealthWarningSigns     *[]string
	HealthConcernLevel     *string
	FeedingNeed            *string
	FeedingPerformed       *string
	FeedTypes              *[]string
	Season                 *string
	SeasonalStoreReadiness *string
	SeasonalReadiness      *string
	SeasonalConcerns       *[]string
}

type HealthData struct {
	AsOf       time.Time
	State      string
	Coverage   string
	Dimensions []HealthDimensionData
}

type HealthDimensionData struct {
	Dimension string
	State     string
	Coverage  string
	Sources   []HealthEvidenceSourceData
}

type HealthEvidenceSourceData struct {
	InspectionID   uuid.UUID
	InspectionType string
	InspectedAt    string
	Field          string
}

type HealthHistoryData struct {
	AlgorithmVersion string
	From             string
	To               string
	Interval         string
	Points           []HealthHistoryPointData
	Inspections      []HealthHistoryInspectionData
}

type HealthHistoryPointData struct {
	Date       string
	State      string
	Coverage   string
	Dimensions []HealthDimensionData
}

type HealthHistoryInspectionData struct {
	ID   uuid.UUID
	Date string
	Type string
}

type HarvestData struct {
	ID          uuid.UUID
	HiveID      uuid.UUID
	Product     string
	Amount      float64
	Unit        string
	HarvestedAt string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type HarvestTotal struct {
	Product string
	Unit    string
	Amount  float64
}

// Internal inspection-service response contract.
type InspectionReportResponse struct {
	From          string            `json:"from"`
	To            string            `json:"to"`
	Inspections   []InspectionData  `json:"inspections"`
	ColonyHealth  HealthData        `json:"colonyHealth"`
	HealthHistory HealthHistoryData `json:"healthHistory"`
}

// Internal harvest-service response contract.
type HarvestReportResponse struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Harvests []HarvestData  `json:"harvests"`
	Count    int            `json:"count"`
	Totals   []HarvestTotal `json:"totals"`
}
