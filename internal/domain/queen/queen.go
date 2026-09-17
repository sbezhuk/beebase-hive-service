// Package queen holds the Queen entity, marking color rules, and the port
// through which the rest of the application persists and retrieves queens.
package queen

import (
	"time"

	"github.com/google/uuid"
)

// MarkingColor represents the international queen marking color standard.
type MarkingColor string

const (
	ColorBlue   MarkingColor = "blue"
	ColorWhite  MarkingColor = "white"
	ColorYellow MarkingColor = "yellow"
	ColorRed    MarkingColor = "red"
	ColorGreen  MarkingColor = "green"
)

// Standard BeeBase hex colors for queen marking.
const (
	HexBlue   = "#A7C7F7"
	HexWhite  = "#F8F9FA"
	HexYellow = "#FFE08A"
	HexRed    = "#FCA5A5"
	HexGreen  = "#A7D7A2"
)

// ColorInfo groups the semantic color name and its exact BeeBase hex representation.
type ColorInfo struct {
	Color MarkingColor
	Hex   string
}

// ColorForYear returns the international queen marking color and hex representation
// based on the ending digit of the given year.
//
// The 5-year repeating cycle is:
//
//	0, 5 -> Blue   (#A7C7F7)
//	1, 6 -> White  (#F8F9FA)
//	2, 7 -> Yellow (#FFE08A)
//	3, 8 -> Red    (#FCA5A5)
//	4, 9 -> Green  (#A7D7A2)
func ColorForYear(year int) ColorInfo {
	lastDigit := (year % 10 + 10) % 10
	switch lastDigit {
	case 0, 5:
		return ColorInfo{Color: ColorBlue, Hex: HexBlue}
	case 1, 6:
		return ColorInfo{Color: ColorWhite, Hex: HexWhite}
	case 2, 7:
		return ColorInfo{Color: ColorYellow, Hex: HexYellow}
	case 3, 8:
		return ColorInfo{Color: ColorRed, Hex: HexRed}
	case 4, 9:
		return ColorInfo{Color: ColorGreen, Hex: HexGreen}
	default:
		return ColorInfo{Color: ColorBlue, Hex: HexBlue}
	}
}

// Queen represents a queen bee associated with a specific hive.
type Queen struct {
	ID           uuid.UUID
	HiveID       uuid.UUID
	Year         int
	MarkedAt     *time.Time
	IntroducedAt time.Time
	RemovedAt    *time.Time
	Notes        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// New constructs a Queen with a freshly generated ID and timestamps set to now.
func New(hiveID uuid.UUID, year int, markedAt *time.Time, introducedAt time.Time, removedAt *time.Time, notes string) *Queen {
	now := time.Now().UTC()
	return &Queen{
		ID:           uuid.New(),
		HiveID:       hiveID,
		Year:         year,
		MarkedAt:     markedAt,
		IntroducedAt: introducedAt,
		RemovedAt:    removedAt,
		Notes:        notes,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// MarkingColor returns the calculated international marking color for the queen's year.
func (q *Queen) MarkingColor() MarkingColor {
	return ColorForYear(q.Year).Color
}

// MarkingColorHex returns the calculated hex color string for the queen's year.
func (q *Queen) MarkingColorHex() string {
	return ColorForYear(q.Year).Hex
}

// IsCurrent reports whether the queen is currently active in the hive.
func (q *Queen) IsCurrent() bool {
	return q.RemovedAt == nil
}
