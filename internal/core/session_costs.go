package core

// The bounds of a ranked cost list: the sessions, projects and tools of ADR
// 0059 each return at most MaxRanked rows, and ten when the caller names none.
const (
	MaxRanked     = 100
	DefaultRanked = 10
)

// SessionCostQuery asks for the most costly sessions among the calls matching
// the filter. Limit bounds the sessions returned; SessionCosts.SessionCount
// still covers every matching session.
type SessionCostQuery struct {
	Filter CallFilter
	Limit  int
}

// Validate checks the time range and that Limit is from 1 to MaxRanked.
func (q SessionCostQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	if q.Limit < 1 || q.Limit > MaxRanked {
		return ErrInvalidFilter
	}
	return nil
}

// SessionCost is one session of a cost ranking. It carries the numbers of the
// session list and the session view, counted over the matching calls of the
// range, and says whether its cost includes an API-equivalent call.
type SessionCost struct {
	SessionSummary
	// APIEquivalent is true when a priced call of the session went through a
	// client_passthrough provider: part of the cost is what the API would
	// bill, not what the person pays.
	APIEquivalent bool
}

// SessionCosts is the answer to a SessionCostQuery. Sessions are the Limit most
// costly ones: known cost first, highest first, then unknown cost, ties by
// calls (most first) and session key. SessionCount is every session of the
// matching calls, so a reader can say how many the list leaves out. A call
// with no session key is in no session.
type SessionCosts struct {
	Sessions     []SessionCost
	SessionCount int
}
