package core

import (
	"errors"
	"testing"
	"time"
)

func TestMonthlyCostQueryValidate(t *testing.T) {
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }
	// 2023-12-01 to 2026-12-01 is 36 calendar months in UTC.
	from, to := at(2023, 12, 1, 0), at(2026, 12, 1, 0)
	tests := []struct {
		name string
		q    MonthlyCostQuery
		ok   bool
	}{
		{"36 months in UTC", MonthlyCostQuery{Filter: CallFilter{From: from, To: to}, TZ: "UTC"}, true},
		{"37 months in UTC", MonthlyCostQuery{Filter: CallFilter{From: from.AddDate(0, -1, 0), To: to}, TZ: "UTC"}, false},
		// These instants are midnight of December 1 in Auckland (+13) three
		// years apart: 36 months there, and 37 in UTC, where they fall in November.
		{"the month count follows the zone: 36 in Auckland", MonthlyCostQuery{Filter: CallFilter{From: at(2023, 11, 30, 11), To: at(2026, 11, 30, 11)}, TZ: "Pacific/Auckland"}, true},
		{"the month count follows the zone: 37 in UTC", MonthlyCostQuery{Filter: CallFilter{From: at(2023, 11, 30, 11), To: at(2026, 11, 30, 11)}, TZ: "UTC"}, false},
		{"empty zone", MonthlyCostQuery{Filter: CallFilter{From: from, To: to}}, false},
		{"Local is the server's zone, not the database's", MonthlyCostQuery{Filter: CallFilter{From: from, To: to}, TZ: "Local"}, false},
		{"unknown zone", MonthlyCostQuery{Filter: CallFilter{From: from, To: to}, TZ: "Mars/Olympus"}, false},
		{"empty range", MonthlyCostQuery{Filter: CallFilter{From: to, To: from}, TZ: "UTC"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.q.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidFilter) {
				t.Errorf("Validate() = %v, want ErrInvalidFilter", err)
			}
		})
	}
}
