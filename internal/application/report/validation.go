// Package report contains the backend contract shared by the Hive Report
// transport and application layers.
package report

import (
	"fmt"
	"strings"
	"time"
)

const (
	LocaleEN = "en"
	LocaleUK = "uk"

	// MaxPeriodMonths is the largest report period accepted by the backend.
	MaxPeriodMonths = 12
)

// Period is an inclusive, UTC calendar-date interval. Reports deliberately
// use dates rather than timestamps so the report has the same boundaries as
// the existing inspection and harvest APIs.
type Period struct {
	From time.Time
	To   time.Time
}

// ParsePeriod parses the public YYYY-MM-DD query parameters and validates the
// inclusive interval. The exact twelve-month boundary is valid: 2026-01-01
// through 2027-01-01 is 12 months, while 2026-01-01 through 2027-01-02 is
// not.
func ParsePeriod(fromValue, toValue string) (Period, error) {
	from, err := parseDate("from", fromValue)
	if err != nil {
		return Period{}, err
	}
	to, err := parseDate("to", toValue)
	if err != nil {
		return Period{}, err
	}
	if from.After(to) {
		return Period{}, fmt.Errorf("from must not be after to")
	}
	if to.After(from.AddDate(0, MaxPeriodMonths, 0)) {
		return Period{}, fmt.Errorf("report period must not exceed %d months", MaxPeriodMonths)
	}
	return Period{From: from, To: to}, nil
}

// ValidateLocale accepts the only two presentation locales supported by v1.
func ValidateLocale(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case LocaleEN, LocaleUK:
		return nil
	default:
		return fmt.Errorf("unsupported locale %q", value)
	}
}

func parseDate(field, value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be a valid YYYY-MM-DD date", field)
	}
	return parsed.UTC(), nil
}
