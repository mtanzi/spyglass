package postgres_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
)

// SizingRowsEnv names the variable that turns on TestUsageSizing: the number of
// generated calls. The test measures the 30-day aggregates that
// docs/architecture/deployment.md, "Sizing", quotes: the usage aggregates
// (ADR 0021) and the five cost queries of the overview (ADR 0059). It is off by
// default because it takes minutes and a few GB of disk.
const sizingRowsEnv = "SPYGLASS_SIZING_ROWS"

func TestUsageSizing(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv(sizingRowsEnv))
	if n <= 0 {
		t.Skipf("set %s (for example 1500000) to measure", sizingRowsEnv)
	}
	s, conn := newStore(t)
	ctx := context.Background()
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	from := to.AddDate(0, 0, -30)

	// Rows spread evenly over the 30 days, with the spread of keys, models,
	// sessions, projects, durations, usage and cost of real traffic. About 3%
	// fail, 2% have no price and 10% have no project.
	seed := time.Now()
	if _, err := conn.Exec(ctx, `
		INSERT INTO calls (id, ts, key_name, client, session_key, family, endpoint, auth_mode, provider, model,
			status, error_class, streamed, duration_ms, ttft_ms, ended_at, compaction, context_compacted,
			input_uncached, cache_read, cache_write_5m, cache_write_1h, output, reasoning,
			usage_raw, usage_estimated, extraction_partial, cost_usd, project)
		SELECT gen_random_uuid(), $2::timestamptz + (($3::timestamptz - $2::timestamptz) / $1::int) * i,
			'key' || (i % 8), 'claude-code', 'session-' || (i % 4000), 'anthropic', '/v1/messages', 'proxy_key', 'anthropic',
			(ARRAY['claude-opus-5-5','claude-sonnet-5-5','claude-haiku-5-5'])[1 + i % 3],
			CASE WHEN i % 33 = 0 THEN 529 ELSE 200 END, CASE WHEN i % 33 = 0 THEN 'overloaded' END,
			true, (200 + (i * 7919) % 30000)::int, 300, $2::timestamptz, false, false,
			(i * 31) % 2000, (i * 17) % 90000, (i * 13) % 4000, (i * 5) % 1000, (i * 11) % 3000, (i * 3) % 500,
			'{"input_tokens":1}', i % 50 = 0, false,
			CASE WHEN i % 50 = 1 THEN NULL ELSE ((i * 37) % 500000)::numeric / 1000000 END,
			CASE WHEN i % 10 = 9 THEN NULL ELSE 'project-' || (i % 40) END
		FROM generate_series(0::bigint, $1::bigint - 1) AS i`, n, from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `VACUUM ANALYZE calls`); err != nil {
		t.Fatal(err)
	}
	// Tool events for the tools query: 60% of the calls request 1 to 3 tool uses
	// (two on average) of 12 tools, and each use has a result half 2 s later, so
	// there are about 2.4 events per call.
	toolSeed := time.Now()
	if _, err := conn.Exec(ctx, `
		INSERT INTO tool_events (call_id, idx, ts, kind, source, tool_call_id, tool_name, bytes, is_error, session_key)
		SELECT c.id, h.idx, c.ts + h.lag, h.kind, 'client', 'toolu_' || c.i || '_' || h.j,
			CASE WHEN h.kind = 'use' THEN 'tool-' || ((c.i + h.j * 5) % 12) END,
			100 + (c.i * 13 + h.j) % 5000, CASE WHEN h.kind = 'result' THEN (c.i + h.j) % 20 = 0 END, c.session_key
		FROM (SELECT id, ts, session_key, row_number() OVER (ORDER BY ts) - 1 AS i FROM calls) c
		CROSS JOIN LATERAL (
			SELECT j, j AS idx, 'use' AS kind, interval '0' AS lag FROM generate_series(0, c.i % 3) AS j
			UNION ALL
			SELECT j, j + 1 + c.i % 3, 'result', interval '2 seconds' FROM generate_series(0, c.i % 3) AS j
		) h
		WHERE c.i % 10 < 6`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `VACUUM ANALYZE tool_events`); err != nil {
		t.Fatal(err)
	}
	var toolEvents int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM tool_events`).Scan(&toolEvents); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d tool events in %v", toolEvents, time.Since(toolSeed).Round(time.Second))

	var size, tableSize string
	if err := conn.QueryRow(ctx, `SELECT pg_size_pretty(pg_total_relation_size('calls')), pg_size_pretty(pg_relation_size('calls'))`).Scan(&size, &tableSize); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d rows in %v; calls is %s with indexes, %s heap", n, time.Since(seed).Round(time.Second), size, tableSize)

	// measure runs fn five times and logs the fastest, median and slowest time.
	// The time is time.Since around the call, so it includes the round trip.
	// fn returns the number of rows (buckets) it got, which the log shows.
	measure := func(name string, fn func() (int, error)) {
		t.Helper()
		var runs []time.Duration
		rows := 0
		for range 5 {
			start := time.Now()
			n, err := fn()
			if err != nil {
				t.Fatal(err)
			}
			runs = append(runs, time.Since(start))
			rows = n
			if rows == 0 {
				// A query that finds nothing measures nothing: the seed is wrong.
				t.Fatalf("%s returned no rows", name)
			}
		}
		slices.Sort(runs)
		t.Logf("%-34s rows=%-6d min %v median %v max %v",
			name, rows, runs[0].Round(time.Millisecond), runs[2].Round(time.Millisecond), runs[4].Round(time.Millisecond))
	}

	for _, c := range []struct {
		group core.UsageGroup
		grain core.Grain
	}{
		{core.GroupNone, core.GrainDay},
		{core.GroupNone, core.GrainHour},
		{core.GroupKey, core.GrainDay},
		{core.GroupModel, core.GrainDay},
		{core.GroupSession, core.GrainDay},
	} {
		q := core.UsageQuery{Filter: core.CallFilter{From: from, To: to}, GroupBy: c.group, Grain: c.grain}
		measure(fmt.Sprintf("usage group=%q grain=%s", c.group, c.grain), func() (int, error) {
			got, err := s.Usage(ctx, q)
			return len(got), err
		})
	}

	// The five cost queries of the overview (ADR 0059), over the same 30 days,
	// for a zone with a daylight saving change as well as UTC where the zone
	// matters, and with the limits the overview uses.
	f := core.CallFilter{From: from, To: to}
	for _, tz := range []string{"UTC", "Europe/Rome"} {
		measure("months tz="+tz, func() (int, error) {
			got, err := s.MonthlyCosts(ctx, core.MonthlyCostQuery{Filter: f, TZ: tz})
			return len(got), err
		})
		measure("punchcard tz="+tz, func() (int, error) {
			got, err := s.Punchcard(ctx, core.PunchcardQuery{Filter: f, TZ: tz})
			return len(got), err
		})
	}
	measure("projects limit=10", func() (int, error) {
		got, err := s.ProjectCosts(ctx, core.ProjectCostQuery{Filter: f, Limit: 10})
		return len(got.Projects), err
	})
	measure("sessions limit=5", func() (int, error) {
		got, err := s.SessionCosts(ctx, core.SessionCostQuery{Filter: f, Limit: 5})
		return len(got.Sessions), err
	})
	measure("tools limit=10", func() (int, error) {
		got, err := s.ToolCosts(ctx, core.ToolCostQuery{Filter: f, Limit: 10})
		return len(got.Tools), err
	})
	// The tools panel takes every call-log filter; one session is the narrow case.
	one := f
	one.SessionKey = "session-3"
	measure("tools limit=10 session", func() (int, error) {
		got, err := s.ToolCosts(ctx, core.ToolCostQuery{Filter: one, Limit: 10})
		return len(got.Tools), err
	})
}
