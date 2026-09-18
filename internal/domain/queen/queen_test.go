package queen_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sbezhuk/beebase-hive-service/internal/domain/queen"
)

func TestColorForYear_ExactSpecification(t *testing.T) {
	tests := []struct {
		year    int
		wantCol queen.MarkingColor
		wantHex string
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
	markedAt2026 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	introducedAt := time.Now().UTC()
	q := queen.New(hiveID, markedAt2026, introducedAt, nil, nil, "Strong layer")

	if !q.IsCurrent() {
		t.Errorf("New queen should be current (RemovedAt is nil)")
	}
	if q.Year() != 2026 {
		t.Errorf("Year() = %d, want 2026 (derived from MarkedAt)", q.Year())
	}
	if q.MarkingColor() != queen.ColorWhite {
		t.Errorf("got %s, want %s", q.MarkingColor(), queen.ColorWhite)
	}
	if q.MarkingColorHex() != queen.HexWhite {
		t.Errorf("got %s, want %s", q.MarkingColorHex(), queen.HexWhite)
	}

	// Change marked year to 2027: color must follow MarkedAt, not IntroducedAt.
	q.MarkedAt = time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	if q.Year() != 2027 {
		t.Errorf("Year() = %d, want 2027", q.Year())
	}
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

// TestQueen_MarkedAtIndependentOfIntroducedAt proves a queen may be marked
// (raised) before she is introduced into this specific hive: the two dates
// are unrelated, and Year()/marking color follow MarkedAt only.
func TestQueen_MarkedAtIndependentOfIntroducedAt(t *testing.T) {
	hiveID := uuid.New()
	markedAt := time.Date(2025, 6, 10, 0, 0, 0, 0, time.UTC)
	introducedAt := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	q := queen.New(hiveID, markedAt, introducedAt, nil, nil, "")

	if q.Year() != 2025 {
		t.Errorf("Year() = %d, want 2025 (MarkedAt's year)", q.Year())
	}
	if q.MarkingColor() != queen.ColorBlue {
		t.Errorf("MarkingColor() = %s, want %s (2025's color)", q.MarkingColor(), queen.ColorBlue)
	}
	if !q.IntroducedAt.Equal(introducedAt) {
		t.Errorf("IntroducedAt = %v, want %v (independent of MarkedAt)", q.IntroducedAt, introducedAt)
	}
}

func TestReplacementReason_IsValid(t *testing.T) {
	validReasons := []queen.ReplacementReason{
		queen.ReasonAgingAndWear,
		queen.ReasonLowEggLaying,
		queen.ReasonInjuryOrMutilation,
		queen.ReasonDiseaseOrPoorQuality,
		queen.ReasonNaturalSupersedure,
		queen.ReasonBreedChangeOrAggressiveness,
	}

	for _, r := range validReasons {
		if !r.IsValid() {
			t.Errorf("expected %q to be valid", r)
		}
	}

	invalidReasons := []queen.ReplacementReason{
		"",
		"aging_and_wear",
		"UNKNOWN",
		"SWARMING",
		"OTHER",
	}

	for _, r := range invalidReasons {
		if r.IsValid() {
			t.Errorf("expected %q to be invalid", r)
		}
	}
}

func TestIsFutureCalendarDate(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"today midnight UTC is not future", today, false},
		{"today with a later time-of-day is not future", today.Add(23*time.Hour + 59*time.Minute), false},
		{"yesterday is not future", today.AddDate(0, 0, -1), false},
		{"far past is not future", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"tomorrow is future", today.AddDate(0, 0, 1), true},
		{"far future is future", today.AddDate(5, 0, 0), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := queen.IsFutureCalendarDate(tc.t); got != tc.want {
				t.Errorf("IsFutureCalendarDate(%v) = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}
