package queen_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

func TestColorForYear_ExactSpecification(t *testing.T) {
	tests := []struct {
		year     int
		wantCol  queen.MarkingColor
		wantHex  string
	}{
		{2024, queen.ColorGreen, queen.HexGreen},
		{2025, queen.ColorBlue, queen.HexBlue},
		{2026, queen.ColorWhite, queen.HexWhite},
		{2027, queen.ColorYellow, queen.HexYellow},
		{2028, queen.ColorRed, queen.HexRed},
		{2029, queen.ColorGreen, queen.HexGreen},
		{2030, queen.ColorBlue, queen.HexBlue},
		{2031, queen.ColorWhite, queen.HexWhite},
	}

	for _, tc := range tests {
		got := queen.ColorForYear(tc.year)
		if got.Color != tc.wantCol {
			t.Errorf("ColorForYear(%d).Color = %q, want %q", tc.year, got.Color, tc.wantCol)
		}
		if got.Hex != tc.wantHex {
			t.Errorf("ColorForYear(%d).Hex = %q, want %q", tc.year, got.Hex, tc.wantHex)
		}
	}
}

func TestColorForYear_CycleAcrossDecades(t *testing.T) {
	decades := []int{1900, 1950, 1980, 2000, 2010, 2020, 2030, 2050, 2090, 2100, 2500}

	for _, decade := range decades {
		for i := 0; i < 10; i++ {
			year := decade + i
			info := queen.ColorForYear(year)
			switch i % 5 {
			case 0:
				if info.Color != queen.ColorBlue || info.Hex != queen.HexBlue {
					t.Errorf("year %d ending in %d: got (%s, %s), want (blue, %s)", year, i, info.Color, info.Hex, queen.HexBlue)
				}
			case 1:
				if info.Color != queen.ColorWhite || info.Hex != queen.HexWhite {
					t.Errorf("year %d ending in %d: got (%s, %s), want (white, %s)", year, i, info.Color, info.Hex, queen.HexWhite)
				}
			case 2:
				if info.Color != queen.ColorYellow || info.Hex != queen.HexYellow {
					t.Errorf("year %d ending in %d: got (%s, %s), want (yellow, %s)", year, i, info.Color, info.Hex, queen.HexYellow)
				}
			case 3:
				if info.Color != queen.ColorRed || info.Hex != queen.HexRed {
					t.Errorf("year %d ending in %d: got (%s, %s), want (red, %s)", year, i, info.Color, info.Hex, queen.HexRed)
				}
			case 4:
				if info.Color != queen.ColorGreen || info.Hex != queen.HexGreen {
					t.Errorf("year %d ending in %d: got (%s, %s), want (green, %s)", year, i, info.Color, info.Hex, queen.HexGreen)
				}
			}
		}
	}
}

func TestQueen_Methods(t *testing.T) {
	hiveID := uuid.New()
	now := time.Now().UTC()
	q := queen.New(hiveID, 2026, &now, now, nil, "Strong layer")

	if !q.IsCurrent() {
		t.Errorf("New queen should be current (RemovedAt is nil)")
	}
	if q.MarkingColor() != queen.ColorWhite {
		t.Errorf("got %s, want %s", q.MarkingColor(), queen.ColorWhite)
	}
	if q.MarkingColorHex() != queen.HexWhite {
		t.Errorf("got %s, want %s", q.MarkingColorHex(), queen.HexWhite)
	}

	// Change year to 2027
	q.Year = 2027
	if q.MarkingColor() != queen.ColorYellow {
		t.Errorf("got %s, want %s", q.MarkingColor(), queen.ColorYellow)
	}
	if q.MarkingColorHex() != queen.HexYellow {
		t.Errorf("got %s, want %s", q.MarkingColorHex(), queen.HexYellow)
	}

	// Mark removed
	removed := time.Now().UTC()
	q.RemovedAt = &removed
	if q.IsCurrent() {
		t.Errorf("Queen with RemovedAt should not be current")
	}
}
