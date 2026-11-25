package core

// PunchcardQuery asks for the cost of the matching calls by weekday and hour of
// day, with both read on the wall clock of the zone TZ (an IANA name). TZ is
// required: the API layer defaults it to "UTC".
type PunchcardQuery struct {
	Filter CallFilter
	TZ     string
}

// Validate checks the time range and the zone.
func (q PunchcardQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	_, err := LoadZone(q.TZ)
	return err
}

// PunchcardCell is the cost of the calls that fall in one weekday and hour of
// day. A cell is a local clock reading ("Tuesday, 14:00 to 14:59"), so on a DST
// change the repeated hour adds into one cell and the skipped hour adds nothing.
type PunchcardCell struct {
	Weekday int // 1 (Monday) to 7 (Sunday)
	Hour    int // 0 to 23
	Calls   int
	CostTotals
}
