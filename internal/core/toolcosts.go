package core

// ToolCostQuery asks for the attributed cost of each tool over the calls
// matching the filter. Limit bounds the tools returned, most costly first; the
// tool count in the result still covers every tool of the matching calls.
//
// The range and the other filters select calls by their own time, not uses by
// theirs (ADR 0059): a use counts when the call that requested it matches.
type ToolCostQuery struct {
	Filter CallFilter
	Limit  int
}

// Validate checks the time range and that Limit is from 1 to MaxRanked.
func (q ToolCostQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	if q.Limit < 1 || q.Limit > MaxRanked {
		return ErrInvalidFilter
	}
	return nil
}

// ToolCost is what the calls that requested one tool spent on it. A tool has
// no cost of its own, because it runs in the client: the cost of a model call
// is split equally among the tool uses the call requested, and a tool's cost
// is the sum of its shares. See ADR 0059 for the rounding rule.
//
// Unknown is not zero: Cost is nil when every call that requested the tool is
// unpriced, and UnpricedCalls counts the distinct unpriced calls that requested
// it at least once.
type ToolCost struct {
	Tool        string
	Uses        int
	Errors      int   // uses whose paired result is an error
	ResultBytes int64 // size of the paired results
	CostTotals
}

// ToolCosts is the answer to a ToolCostQuery. Tools are the Limit most costly
// ones: known cost first, highest first, then unknown cost, ties by uses (most
// first) and name. ToolCount is every tool of the matching calls.
type ToolCosts struct {
	Tools     []ToolCost
	ToolCount int
}
