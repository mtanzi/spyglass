package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/mtanzi/spyglass/internal/core"
)

// cellRange covers all of 2026, so the DST changes of March and November fall in it.
var cellRange = core.CallFilter{From: utc(2026, 1, 1, 0, 0), To: utc(2027, 1, 1, 0, 0)}

// wantCell is one expected punchcard cell. cost -1 means an unknown (nil) cost.
type wantCell struct {
	weekday, hour, calls int
	cost                 int64
	unpriced             int
}

func checkCells(t *testing.T, got []core.PunchcardCell, want []wantCell) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d cells %+v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		g := got[i]
		known := g.Cost != nil
		if g.Weekday != w.weekday || g.Hour != w.hour || g.Calls != w.calls || known != (w.cost >= 0) ||
			(known && int64(*g.Cost) != w.cost) || g.UnpricedCalls != w.unpriced {
			t.Errorf("cell %d = %+v (cost %v), want weekday %d hour %d calls %d cost %d unpriced %d",
				i, g, g.Cost, w.weekday, w.hour, w.calls, w.cost, w.unpriced)
		}
	}
}

func punchcard(t *testing.T, e *env, f core.CallFilter, tz string) []core.PunchcardCell {
	t.Helper()
	got, err := e.Punchcard(context.Background(), core.PunchcardQuery{Filter: f, TZ: tz})
	if err != nil {
		t.Fatalf("%s: %v", tz, err)
	}
	return got
}

var punchcardCases = []testCase{
	{"punchcard_reads_the_wall_clock_of_a_half_hour_zone", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			// Monday 2026-11-23 18:29 UTC is 23:59 in Kolkata (UTC+5:30), still Monday.
			g.Call(g.At(utc(2026, 11, 23, 18, 29)), WithCost(1)),
			// One minute later it is Tuesday 00:00.
			g.Call(g.At(utc(2026, 11, 23, 18, 30)), WithCost(2)),
			// Tuesday 2026-11-24 08:45 UTC is 14:15 in Kolkata; so is 14:59 (09:29 UTC).
			g.Call(g.At(utc(2026, 11, 24, 8, 45)), WithCost(4)),
			g.Call(g.At(utc(2026, 11, 24, 9, 29)), WithCost(8)),
		)
		checkCells(t, punchcard(t, e, cellRange, "Asia/Kolkata"), []wantCell{
			{1, 23, 1, 1, 0}, {2, 0, 1, 2, 0}, {2, 14, 2, 12, 0},
		})
		// The same calls on the UTC clock.
		checkCells(t, punchcard(t, e, cellRange, "UTC"), []wantCell{
			{1, 18, 2, 3, 0}, {2, 8, 1, 4, 0}, {2, 9, 1, 8, 0},
		})
	}},
	{"punchcard_numbers_sunday_7_and_orders_by_weekday_then_hour", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			g.Call(g.At(utc(2026, 11, 29, 12, 0)), WithCost(1)), // Sunday
			g.Call(g.At(utc(2026, 11, 23, 23, 0)), WithCost(2)), // Monday
			g.Call(g.At(utc(2026, 11, 29, 3, 0)), WithCost(4)),  // Sunday, earlier
			g.Call(g.At(utc(2026, 11, 23, 1, 0)), WithCost(8)),  // Monday, earlier
		)
		checkCells(t, punchcard(t, e, cellRange, "UTC"), []wantCell{
			{1, 1, 1, 8, 0}, {1, 23, 1, 2, 0}, {7, 3, 1, 4, 0}, {7, 12, 1, 1, 0},
		})
	}},
	{"punchcard_adds_the_repeated_hour_into_one_cell_and_skips_the_missing_one", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			// Fall back in Los Angeles, Sunday 2026-11-01: 01:30 happens twice,
			// at 08:30 UTC (PDT) and at 09:30 UTC (PST).
			g.Call(g.At(utc(2026, 11, 1, 8, 30)), WithCost(1)),
			g.Call(g.At(utc(2026, 11, 1, 9, 30)), WithCost(2)),
			// Spring forward, Sunday 2026-03-08: 02:00 to 02:59 does not exist.
			// 09:30 UTC is 01:30 PST and 10:30 UTC is 03:30 PDT.
			g.Call(g.At(utc(2026, 3, 8, 9, 30)), WithCost(4)),
			g.Call(g.At(utc(2026, 3, 8, 10, 30)), WithCost(8)),
		)
		checkCells(t, punchcard(t, e, cellRange, "America/Los_Angeles"), []wantCell{
			{7, 1, 3, 7, 0}, // both 01:30 readings of November 1, and 01:30 of March 8
			{7, 3, 1, 8, 0}, // there is no hour 2 on Sundays
		})
	}},
	{"punchcard_keeps_unknown_cost_apart_from_zero", func(t *testing.T, e *env) {
		g := e.gen
		pass := WithAuthMode("client_passthrough")
		e.write(t,
			// Monday 01:00 UTC: two priced calls and one unpriced.
			g.Call(g.At(utc(2026, 11, 23, 1, 0)), WithCost(100)), g.Call(g.At(utc(2026, 11, 23, 1, 10)), WithCost(50)),
			g.Call(g.At(utc(2026, 11, 23, 1, 20)), WithoutCost()),
			// Monday 02:00: only unpriced calls.
			g.Call(g.At(utc(2026, 11, 23, 2, 0)), WithoutCost()), g.Call(g.At(utc(2026, 11, 23, 2, 10)), WithoutCost()),
			// Monday 03:00: a priced call of cost zero is known, and a priced passthrough call is API-equivalent.
			g.Call(g.At(utc(2026, 11, 23, 3, 0)), WithCost(0)), g.Call(g.At(utc(2026, 11, 23, 3, 10)), WithCost(7), pass),
			// Monday 04:00: an unpriced passthrough call has no cost to mark.
			g.Call(g.At(utc(2026, 11, 23, 4, 0)), WithoutCost(), pass),
		)
		got := punchcard(t, e, cellRange, "UTC")
		checkCells(t, got, []wantCell{{1, 1, 3, 150, 1}, {1, 2, 2, -1, 2}, {1, 3, 2, 7, 0}, {1, 4, 1, -1, 1}})
		for i, want := range []bool{false, false, true, false} {
			if got[i].APIEquivalent != want {
				t.Errorf("cell %d api_equivalent = %v, want %v", i, got[i].APIEquivalent, want)
			}
		}
	}},
	{"punchcard_applies_the_range_and_the_call_filter_and_sums_to_the_usage_total", func(t *testing.T, e *env) {
		g := e.gen
		e.write(t,
			g.Call(g.At(utc(2026, 11, 3, 10, 0)), WithCost(100), WithKey("bob")),
			g.Call(g.At(utc(2026, 11, 4, 10, 0)), WithCost(40), WithKey("alice")),
			g.Call(g.At(utc(2026, 11, 10, 22, 0)), WithCost(9), WithKey("bob")),
			g.Call(g.At(utc(2026, 11, 11, 22, 0)), WithoutCost(), WithKey("bob")),
			g.Call(g.At(utc(2026, 12, 20, 5, 0)), WithCost(1000), WithKey("bob")), // outside the range
		)
		ctx := context.Background()
		f := core.CallFilter{From: utc(2026, 11, 1, 0, 0), To: utc(2026, 12, 1, 0, 0)}
		fb := f
		fb.Keys = []string{"bob"}
		checkCells(t, punchcard(t, e, fb, "UTC"), []wantCell{{2, 10, 1, 100, 0}, {2, 22, 1, 9, 0}, {3, 22, 1, -1, 1}})

		// With no filter the cells add up to the calls and cost of the usage query.
		cells := punchcard(t, e, f, "Pacific/Auckland")
		buckets, err := e.Usage(ctx, core.UsageQuery{Filter: f, Grain: core.GrainDay})
		if err != nil {
			t.Fatal(err)
		}
		var calls, usageCalls int
		var cost, usageCost core.MicroUSD
		for _, c := range cells {
			calls += c.Calls
			if c.Cost != nil {
				cost += *c.Cost
			}
		}
		for _, b := range buckets {
			usageCalls += b.Calls
			usageCost += b.Cost
		}
		if calls != usageCalls || cost != usageCost || calls != 4 || cost != 149 {
			t.Errorf("cells hold %d calls and %d, usage %d calls and %d; want 4 calls and 149", calls, cost, usageCalls, usageCost)
		}
	}},
	{"punchcard_has_no_cells_for_no_calls", func(t *testing.T, e *env) {
		if got := punchcard(t, e, cellRange, "UTC"); len(got) != 0 {
			t.Errorf("cells = %+v, want none", got)
		}
	}},
	{"punchcard_rejects_a_bad_query", func(t *testing.T, e *env) {
		for _, q := range []core.PunchcardQuery{
			{Filter: core.CallFilter{}, TZ: "UTC"},
			{Filter: core.CallFilter{From: cellRange.To, To: cellRange.From}, TZ: "UTC"},
			{Filter: cellRange, TZ: "Not/AZone"},
			{Filter: cellRange, TZ: ""},
			{Filter: cellRange, TZ: "Local"},
			{Filter: cellRange, TZ: "UTC'; DROP TABLE calls; --"},
		} {
			if _, err := e.Punchcard(context.Background(), q); !errors.Is(err, core.ErrInvalidFilter) {
				t.Errorf("Punchcard(%+v) err = %v", q, err)
			}
		}
	}},
}
