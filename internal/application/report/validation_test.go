package report

import (
	"testing"
)

func TestParsePeriodAcceptsExactTwelveMonthBoundary(t *testing.T) {
	period, err := ParsePeriod("2026-01-01", "2027-01-01")
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	if period.From.Format("2006-01-02") != "2026-01-01" || period.To.Format("2006-01-02") != "2027-01-01" {
		t.Fatalf("period = %+v", period)
	}
}

func TestParsePeriodRejectsMoreThanTwelveMonths(t *testing.T) {
	if _, err := ParsePeriod("2026-01-01", "2027-01-02"); err == nil {
		t.Fatal("ParsePeriod accepted a period longer than twelve months")
	}
}

func TestParsePeriodRejectsReversedAndMalformedDates(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
	}{
		{name: "reversed", from: "2026-02-01", to: "2026-01-31"},
		{name: "malformed from", from: "2026-1-01", to: "2026-01-02"},
		{name: "malformed to", from: "2026-01-01", to: "not-a-date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParsePeriod(tt.from, tt.to); err == nil {
				t.Fatal("ParsePeriod accepted invalid input")
			}
		})
	}
}

func TestValidateLocale(t *testing.T) {
	for _, locale := range []string{"en", "uk", "EN", " uk "} {
		if err := ValidateLocale(locale); err != nil {
			t.Errorf("ValidateLocale(%q): %v", locale, err)
		}
	}
	if err := ValidateLocale("de"); err == nil {
		t.Fatal("ValidateLocale accepted unsupported locale")
	}
}
