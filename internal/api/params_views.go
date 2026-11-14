package api

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// DefaultGrain is the usage bucket width of a query that names none.
const DefaultGrain = core.GrainHour

// The group_by and grain whitelists of GET /admin/usage. They are written out
// here, not derived from core, so a value added to core is not served until
// the API document says so.
var (
	usageGroups = []core.UsageGroup{
		core.GroupKey, core.GroupModel, core.GroupClient, core.GroupProvider, core.GroupSession, core.GroupErrorClass,
	}
	usageGrains = []core.Grain{core.GrainMinute, core.GrainHour, core.GrainDay}

	toolSources  = []core.ToolSource{core.ToolSourceClient, core.ToolSourceServer}
	toolStatuses = []core.ToolCallStatus{core.ToolCallPaired, core.ToolCallUseOnly, core.ToolCallResultOnly, core.ToolCallPending}
)

var knownSessionParams = map[string]bool{
	"from": true, "to": true, "key": true, "client": true, "provider": true, "model": true,
	"error_class": true, "session": true, "cost_micro_usd_gte": true, "limit": true, "cursor": true,
}

var knownToolCallParams = map[string]bool{
	"from": true, "to": true, "session": true, "agent": true, "tool": true, "source": true, "status": true,
	"is_error": true, "duration_ms_gte": true, "arg_bytes_gte": true, "result_bytes_gte": true,
	"limit": true, "cursor": true,
}

// The tool summary takes the tool-call filter and no page.
var knownToolSummaryParams = func() map[string]bool {
	m := map[string]bool{}
	for k := range knownToolCallParams {
		if k != "limit" && k != "cursor" {
			m[k] = true
		}
	}
	return m
}()

// The usage query takes the call-log filter and no page, plus group_by and grain.
var knownUsageParams = func() map[string]bool {
	m := map[string]bool{"group_by": true, "grain": true}
	for k := range knownCallParams {
		if k != "limit" && k != "cursor" {
			m[k] = true
		}
	}
	return m
}()

// The prompt-cost query takes the call-log filter and a limit, and no cursor.
var knownPromptCostParams = func() map[string]bool {
	m := map[string]bool{}
	for k := range knownCallParams {
		if k != "cursor" {
			m[k] = true
		}
	}
	return m
}()

// parsePromptCostParams maps a query string onto a core.PromptCostQuery. limit
// is optional, from 1 to MaxLimit, and defaults to DefaultLimit.
func parsePromptCostParams(q url.Values, now time.Time) (core.PromptCostQuery, *apiError) {
	var pq core.PromptCostQuery
	var err *apiError
	if pq.Filter, err = parseCallFilter(q, now, knownPromptCostParams); err != nil {
		return pq, err
	}
	limit, err := intParam(q, "limit", 1, MaxLimit)
	if err != nil {
		return pq, err
	}
	pq.Limit = int(limit)
	if _, set := q["limit"]; !set {
		pq.Limit = DefaultLimit
	}
	return pq, nil
}

// sessionParams is a parsed GET /admin/sessions query.
type sessionParams struct {
	Filter core.SessionFilter
	Page   core.Page
}

// parseSessionParams maps a query string onto a core.SessionFilter and a page,
// with the rules of parseCallParams.
func parseSessionParams(q url.Values, now time.Time) (sessionParams, *apiError) {
	var p sessionParams
	if err := checkKnown(q, knownSessionParams); err != nil {
		return p, err
	}
	f := &p.Filter
	var err *apiError
	if f.From, f.To, err = timeRange(q, now); err != nil {
		return p, err
	}
	f.Keys = q["key"]
	f.Clients = q["client"]
	f.Providers = q["provider"]
	f.Models = q["model"]
	f.ErrorClasses = q["error_class"]
	if err := nonEmpty(q, "key", "client", "provider", "model", "error_class"); err != nil {
		return p, err
	}
	if f.SessionKey, err = stringParam(q, "session"); err != nil {
		return p, err
	}
	cost, err := intParam(q, "cost_micro_usd_gte", 0, 1<<62)
	if err != nil {
		return p, err
	}
	f.MinCost = core.MicroUSD(cost)
	p.Page, err = parsePage(q)
	return p, err
}

// toolCallParams is a parsed GET /admin/tool-calls query.
type toolCallParams struct {
	Filter core.ToolCallFilter
	Page   core.Page
}

// parseToolCallParams maps a query string onto a core.ToolCallFilter and a page.
func parseToolCallParams(q url.Values, now time.Time) (toolCallParams, *apiError) {
	var p toolCallParams
	var err *apiError
	if p.Filter, err = parseToolCallFilter(q, now, knownToolCallParams); err != nil {
		return p, err
	}
	p.Page, err = parsePage(q)
	return p, err
}

// parseToolCallFilter maps the tool-call filter parameters of q, which must all
// be in known. The tool-call list and its summary share it.
func parseToolCallFilter(q url.Values, now time.Time, known map[string]bool) (core.ToolCallFilter, *apiError) {
	var f core.ToolCallFilter
	if err := checkKnown(q, known); err != nil {
		return f, err
	}
	var err *apiError
	if f.From, f.To, err = timeRange(q, now); err != nil {
		return f, err
	}
	if f.SessionKey, err = stringParam(q, "session"); err != nil {
		return f, err
	}
	if f.AgentID, err = stringParam(q, "agent"); err != nil {
		return f, err
	}
	f.Tools = q["tool"]
	if err := nonEmpty(q, "tool"); err != nil {
		return f, err
	}
	if f.Sources, err = enumParams(q, "source", toolSources); err != nil {
		return f, err
	}
	if f.Statuses, err = enumParams(q, "status", toolStatuses); err != nil {
		return f, err
	}
	if f.IsError, err = boolParam(q, "is_error"); err != nil {
		return f, err
	}
	ms, err := intParam(q, "duration_ms_gte", 0, 1<<40)
	if err != nil {
		return f, err
	}
	f.MinDuration = time.Duration(ms) * time.Millisecond
	arg, err := intParam(q, "arg_bytes_gte", 0, 1<<31-1)
	if err != nil {
		return f, err
	}
	f.MinArgBytes = int(arg)
	res, err := intParam(q, "result_bytes_gte", 0, 1<<31-1)
	if err != nil {
		return f, err
	}
	f.MinResultBytes = int(res)
	return f, nil
}

// parseUsageParams maps a query string onto a core.UsageQuery. group_by is
// optional and means no grouping; grain defaults to DefaultGrain. Both are
// checked against the whitelists above, and a value outside them is an
// invalid_parameter error that names the allowed values.
func parseUsageParams(q url.Values, now time.Time) (core.UsageQuery, *apiError) {
	var uq core.UsageQuery
	var err *apiError
	if uq.Filter, err = parseCallFilter(q, now, knownUsageParams); err != nil {
		return uq, err
	}
	groups, err := enumParams(q, "group_by", usageGroups)
	if err != nil {
		return uq, err
	}
	if len(groups) > 1 {
		return uq, badParam("group_by", "parameter must not be repeated")
	}
	if len(groups) == 1 {
		uq.GroupBy = groups[0]
	}
	grains, err := enumParams(q, "grain", usageGrains)
	if err != nil {
		return uq, err
	}
	if len(grains) > 1 {
		return uq, badParam("grain", "parameter must not be repeated")
	}
	uq.Grain = DefaultGrain
	if len(grains) == 1 {
		uq.Grain = grains[0]
	}
	return uq, nil
}

// enumParams returns the values of name, each of which must be in allowed.
func enumParams[T ~string](q url.Values, name string, allowed []T) ([]T, *apiError) {
	var out []T
	for _, v := range q[name] {
		if !slices.Contains(allowed, T(v)) {
			names := make([]string, len(allowed))
			for i, a := range allowed {
				names[i] = string(a)
			}
			return nil, badParam(name, fmt.Sprintf("%q is not one of %s", v, strings.Join(names, ", ")))
		}
		out = append(out, T(v))
	}
	return out, nil
}
