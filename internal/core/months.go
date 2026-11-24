package core

import (
	"time"
	// The zone database is compiled in, so a scratch container image with no
	// system zone data still resolves every IANA name (ADR 0059).
	_ "time/tzdata"
)

// MaxMonths is the most calendar months one MonthlyCostQuery may cover.
const MaxMonths = 36

// LoadZone resolves an IANA time zone name such as "Europe/Rome". It returns
// ErrInvalidFilter for an empty or unknown name. "Local" is refused too: it
// names the zone of the server process, which the database does not know.
func LoadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, ErrInvalidFilter
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalidFilter
	}
	return loc, nil
}

// CostTotals is the cost half of an aggregate row. Unknown is not zero: Cost
// sums the priced calls and is nil when no call is priced, and UnpricedCalls
// counts the calls it leaves out.
type CostTotals struct {
	Cost          *MicroUSD
	UnpricedCalls int
	// APIEquivalent is true when a priced call went through a
	// client_passthrough provider: part of the cost is what the API would
	// bill, not what the person pays.
	APIEquivalent bool
}

// MonthlyCostQuery asks for the cost of each calendar month of the filter's
// range, with months cut in the time zone TZ (an IANA name). TZ is required:
// the API layer defaults it to "UTC".
type MonthlyCostQuery struct {
	Filter CallFilter
	TZ     string
}

// Validate checks the time range, the zone, and that the range covers at most
// MaxMonths calendar months in that zone.
func (q MonthlyCostQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	loc, err := LoadZone(q.TZ)
	if err != nil {
		return err
	}
	if len(MonthsOf(q.Filter.From, q.Filter.To, loc, MaxMonths+1)) > MaxMonths {
		return ErrInvalidFilter
	}
	return nil
}

// MonthSpan is one calendar month in a zone.
type MonthSpan struct {
	Label      string    // "2026-11"
	Start, End time.Time // the whole month as UTC instants: [Start, End)
}

// MonthsOf lists the calendar months of loc that [from, to) touches, oldest
// first, including months with no calls. It stops after limit months, so a
// huge range costs no more than limit steps.
func MonthsOf(from, to time.Time, loc *time.Location, limit int) []MonthSpan {
	f := from.In(loc)
	var out []MonthSpan
	for y, m := f.Year(), f.Month(); len(out) < limit; m++ {
		start := time.Date(y, m, 1, 0, 0, 0, 0, loc)
		if !start.Before(to) {
			break
		}
		out = append(out, MonthSpan{
			Label: start.Format("2006-01"),
			Start: start.UTC(),
			End:   time.Date(y, m+1, 1, 0, 0, 0, 0, loc).UTC(),
		})
	}
	return out
}

// MonthCost is the cost of the calls of one calendar month. A month with no
// calls is present with zero calls and a known cost of zero.
type MonthCost struct {
	Month      string    // "2026-11", in the query's zone
	Start, End time.Time // the whole month as UTC instants, even when the range cuts it
	Calls      int
	CostTotals
	Partial bool // the query range cuts the month
}
