package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// monthRange covers October and November 2026 in UTC.
var monthRange = core.CallFilter{From: utc(2026, 10, 1, 0, 0), To: utc(2026, 12, 1, 0, 0)}

// wantMonth is one expected month row. cost -1 means an unknown (nil) cost.
type wantMonth struct {
	month    string
	calls    int
	cost     int64
	unpriced int
	partial  bool
}

func checkMonths(t *testing.T, got []core.MonthCost, want []wantMonth) {
	t.Helper()
	var labels, wantLabels []string
	for _, m := range got {
		labels = append(labels, m.Month)
	}
	for _, w := range want {
		wantLabels = append(wantLabels, w.month)
	}
	if !slices.Equal(labels, wantLabels) {
		t.Fatalf("months %v, want %v", labels, wantLabels)
	}
	for i, w := range want {
		g := got[i]
		known := g.Cost != nil
		if g.Calls != w.calls || known != (w.cost >= 0) || (known && int64(*g.Cost) != w.cost) ||
			g.UnpricedCalls != w.unpriced || g.Partial != w.partial {
			t.Errorf("month %s = %+v (cost %v), want calls %d cost %d unpriced %d partial %v",
				w.month, g, g.Cost, w.calls, w.cost, w.unpriced, w.partial)
		}
	}
}

var monthCases = []testCase{
	{"months_follow_the_zone_of_the_query", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			// 30 minutes before midnight UTC on the last day of October: still
			// October in UTC and Los Angeles (16:30), already November in
			// Auckland (12:30 on the 1st).
			g.Call(g.At(utc(2026, 10, 31, 23, 30)), WithCost(100)),
			g.Call(g.At(utc(2026, 10, 15, 12, 0)), WithCost(10)),
		)
		for _, tc := range []struct {
			tz   string
			want []wantMonth
		}{
			{"UTC", []wantMonth{{"2026-10", 2, 110, 0, false}, {"2026-11", 0, 0, 0, false}}},
			// 2026-10-01T00:00Z is still September 30 in Los Angeles, and the end
			// 2026-12-01T00:00Z is November 30 there: both ends are partial.
			{"America/Los_Angeles", []wantMonth{
				{"2026-09", 0, 0, 0, true}, {"2026-10", 2, 110, 0, false}, {"2026-11", 0, 0, 0, true}}},
			// In Auckland (+13) the range starts on October 1 at 13:00 and ends
			// on December 1 at 13:00, so October and December are cut.
			{"Pacific/Auckland", []wantMonth{
				{"2026-10", 1, 10, 0, true}, {"2026-11", 1, 100, 0, false}, {"2026-12", 0, 0, 0, true}}},
		} {
			got, err := e.MonthlyCosts(context.Background(), core.MonthlyCostQuery{Filter: monthRange, TZ: tc.tz})
			if err != nil {
				t.Fatalf("%s: %v", tc.tz, err)
			}
			t.Run(tc.tz, func(t *testing.T) { checkMonths(t, got, tc.want) })
		}
	}},
	{"months_name_the_whole_month_as_utc_instants", func(t *testing.T, e *env) {
		got, err := e.MonthlyCosts(context.Background(), core.MonthlyCostQuery{
			// A range that starts in the middle of November in Auckland.
			Filter: core.CallFilter{From: utc(2026, 11, 15, 0, 0), To: utc(2026, 11, 20, 0, 0)}, TZ: "Pacific/Auckland"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Month != "2026-11" || !got[0].Partial {
			t.Fatalf("months = %+v, want one partial 2026-11", got)
		}
		// Midnight on November 1 and December 1 in Auckland (UTC+13).
		if want := utc(2026, 10, 31, 11, 0); !got[0].Start.Equal(want) {
			t.Errorf("start = %v, want %v", got[0].Start, want)
		}
		if want := utc(2026, 11, 30, 11, 0); !got[0].End.Equal(want) {
			t.Errorf("end = %v, want %v", got[0].End, want)
		}
	}},
	{"months_start_and_end_follow_a_dst_change", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			// 23:30 on February 28 in Los Angeles (PST, UTC-8) is March 1 in UTC.
			g.Call(g.At(utc(2026, 3, 1, 7, 30)), WithCost(1)),
			// 23:30 on March 31 in Los Angeles (PDT, UTC-7) is April 1 in UTC.
			g.Call(g.At(utc(2026, 4, 1, 6, 30)), WithCost(2)),
		)
		got, err := e.MonthlyCosts(context.Background(), core.MonthlyCostQuery{
			Filter: core.CallFilter{From: utc(2026, 2, 1, 8, 0), To: utc(2026, 4, 1, 7, 0)}, TZ: "America/Los_Angeles"})
		if err != nil {
			t.Fatal(err)
		}
		checkMonths(t, got, []wantMonth{{"2026-02", 1, 1, 0, false}, {"2026-03", 1, 2, 0, false}})
		// March starts on PST and ends on PDT: 743 hours, not 744.
		if !got[1].Start.Equal(utc(2026, 3, 1, 8, 0)) || !got[1].End.Equal(utc(2026, 4, 1, 7, 0)) {
			t.Errorf("March = %v to %v", got[1].Start, got[1].End)
		}
	}},
	{"months_mark_a_range_that_starts_mid_month_as_partial", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			g.Call(g.At(utc(2026, 10, 10, 12, 0)), WithCost(5)), // before from: not counted
			g.Call(g.At(utc(2026, 10, 20, 12, 0)), WithCost(7)),
		)
		f := core.CallFilter{From: utc(2026, 10, 15, 0, 0), To: utc(2026, 12, 1, 0, 0)}
		got, err := e.MonthlyCosts(context.Background(), core.MonthlyCostQuery{Filter: f, TZ: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		checkMonths(t, got, []wantMonth{{"2026-10", 1, 7, 0, true}, {"2026-11", 0, 0, 0, false}})
		if !got[0].Start.Equal(utc(2026, 10, 1, 0, 0)) {
			t.Errorf("a partial month still names the whole month: start %v", got[0].Start)
		}
	}},
	{"months_keep_unknown_cost_apart_from_zero", func(t *testing.T, e *env) {
		g := e.gen
		pass := WithAuthMode("client_passthrough")
		e.write(t,
			// October: two priced calls and one unpriced.
			g.Call(g.At(utc(2026, 10, 3, 0, 0)), WithCost(100)), g.Call(g.At(utc(2026, 10, 4, 0, 0)), WithCost(50)),
			g.Call(g.At(utc(2026, 10, 5, 0, 0)), WithoutCost()),
			// November: only unpriced calls.
			g.Call(g.At(utc(2026, 11, 3, 0, 0)), WithoutCost()), g.Call(g.At(utc(2026, 11, 4, 0, 0)), WithoutCost()),
			// December: a priced call of cost zero is known, and a priced passthrough call is API-equivalent.
			g.Call(g.At(utc(2026, 12, 3, 0, 0)), WithCost(0)), g.Call(g.At(utc(2026, 12, 4, 0, 0)), WithCost(7), pass),
			// January: an unpriced passthrough call has no cost to mark.
			g.Call(g.At(utc(2027, 1, 3, 0, 0)), WithoutCost(), pass),
		)
		f := core.CallFilter{From: utc(2026, 10, 1, 0, 0), To: utc(2027, 2, 1, 0, 0)}
		got, err := e.MonthlyCosts(context.Background(), core.MonthlyCostQuery{Filter: f, TZ: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		checkMonths(t, got, []wantMonth{
			{"2026-10", 3, 150, 1, false}, {"2026-11", 2, -1, 2, false},
			{"2026-12", 2, 7, 0, false}, {"2027-01", 1, -1, 1, false},
		})
		for i, want := range []bool{false, false, true, false} {
			if got[i].APIEquivalent != want {
				t.Errorf("month %s api_equivalent = %v, want %v", got[i].Month, got[i].APIEquivalent, want)
			}
		}
	}},
	{"months_apply_the_call_filter_and_sum_to_the_usage_total", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			g.Call(g.At(utc(2026, 10, 3, 0, 0)), WithCost(100), WithKey("bob")),
			g.Call(g.At(utc(2026, 10, 4, 0, 0)), WithCost(40), WithKey("alice")),
			g.Call(g.At(utc(2026, 11, 4, 0, 0)), WithCost(9), WithKey("bob")),
			g.Call(g.At(utc(2026, 11, 5, 0, 0)), WithoutCost(), WithKey("bob")),
		)
		ctx := context.Background()
		f := monthRange
		f.Keys = []string{"bob"}
		got, err := e.MonthlyCosts(ctx, core.MonthlyCostQuery{Filter: f, TZ: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		checkMonths(t, got, []wantMonth{{"2026-10", 1, 100, 0, false}, {"2026-11", 2, 9, 1, false}})

		// With no filter the months add up to the calls and cost of the usage query.
		got, err = e.MonthlyCosts(ctx, core.MonthlyCostQuery{Filter: monthRange, TZ: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		buckets, err := e.Usage(ctx, core.UsageQuery{Filter: monthRange, Grain: core.GrainDay})
		if err != nil {
			t.Fatal(err)
		}
		var calls, cost, usageCalls int
		var usageCost core.MicroUSD
		for _, m := range got {
			calls += m.Calls
			if m.Cost != nil {
				cost += int(*m.Cost)
			}
		}
		for _, b := range buckets {
			usageCalls += b.Calls
			usageCost += b.Cost
		}
		if calls != usageCalls || core.MicroUSD(cost) != usageCost || calls != 4 || cost != 149 {
			t.Errorf("months hold %d calls and %d, usage %d calls and %d; want 4 calls and 149", calls, cost, usageCalls, usageCost)
		}
	}},
	{"months_accept_36_months_and_refuse_37", func(t *testing.T, e *env) {
		ctx := context.Background()
		got, err := e.MonthlyCosts(ctx, core.MonthlyCostQuery{
			Filter: core.CallFilter{From: utc(2023, 12, 1, 0, 0), To: utc(2026, 12, 1, 0, 0)}, TZ: "UTC"})
		if err != nil || len(got) != 36 || got[0].Month != "2023-12" || got[35].Month != "2026-11" {
			t.Errorf("36 months: %d rows, err %v", len(got), err)
		}
		_, err = e.MonthlyCosts(ctx, core.MonthlyCostQuery{
			Filter: core.CallFilter{From: utc(2023, 11, 1, 0, 0), To: utc(2026, 12, 1, 0, 0)}, TZ: "UTC"})
		if !errors.Is(err, core.ErrInvalidFilter) {
			t.Errorf("37 months: err = %v", err)
		}
	}},
	{"months_reject_a_bad_query", func(t *testing.T, e *env) {
		for _, q := range []core.MonthlyCostQuery{
			{Filter: core.CallFilter{}, TZ: "UTC"},
			{Filter: core.CallFilter{From: monthRange.To, To: monthRange.From}, TZ: "UTC"},
			{Filter: monthRange, TZ: "Not/AZone"},
			{Filter: monthRange, TZ: ""},
			{Filter: monthRange, TZ: "Local"},
			{Filter: monthRange, TZ: "UTC'; DROP TABLE calls; --"},
		} {
			if _, err := e.MonthlyCosts(context.Background(), q); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("MonthlyCosts(%+v) err = %v", q, err)
			}
		}
	}},
}
