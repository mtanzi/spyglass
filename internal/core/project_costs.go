package core

// ProjectCostQuery asks for the most costly projects among the calls matching
// the filter. Limit bounds the projects returned; ProjectCosts.ProjectCount
// still covers every project of the matching calls.
type ProjectCostQuery struct {
	Filter CallFilter
	Limit  int
}

// Validate checks the time range and that Limit is from 1 to MaxRanked.
func (q ProjectCostQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	if q.Limit < 1 || q.Limit > MaxRanked {
		return ErrInvalidFilter
	}
	return nil
}

// ProjectCost is the cost of the matching calls of one project.
type ProjectCost struct {
	Project string
	Calls   int
	// Sessions counts the distinct session keys of the project's calls. A call
	// with no session key is in no session.
	Sessions int
	CostTotals
}

// UnassignedCost is the cost of the matching calls that have no project. It is
// not a project and never ranks. With no such call it has zero calls and a
// known cost of zero; with calls that are all unpriced, Cost is nil.
type UnassignedCost struct {
	Calls         int
	Cost          *MicroUSD
	UnpricedCalls int
}

// ProjectCosts is the answer to a ProjectCostQuery. Projects are the Limit
// most costly ones: known cost first, highest first, then unknown cost, ties by
// calls (most first) and project name. ProjectCount is every project of the
// matching calls, so a reader can say how many the list leaves out; it does not
// count the calls with no project, which are in Unassigned.
type ProjectCosts struct {
	Projects     []ProjectCost
	ProjectCount int
	Unassigned   UnassignedCost
}
