// Package storetest holds the shared contract tests for storage adapters.
//
// The suite is the definition of correct storage behaviour. A new filter, port
// method or behaviour starts as a case here. Cases are grouped; a later ADR
// adds its group to groups and its file next to this one.
package storetest

import (
	"context"
	"testing"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/store/postgres"
)

// group is a named set of cases. Each case gets its own store, from newStore.
type group struct {
	name  string
	cases []testCase
}

type testCase struct {
	name string
	run  func(t *testing.T, s *env)
}

// env is what a case works with: a store on a fresh, migrated database, and a
// record generator.
type env struct {
	*postgres.Store
	gen *Gen
}

// groups lists the case groups, in run order.
var groups = []group{
	{"write", writeCases},
	{"retention", retentionCases},
	{"read", readCases},
	{"tools", toolCases},
	{"sessions", sessionCases},
	{"usage", usageCases},
	{"months", monthCases},
	{"session_costs", sessionCostCases},
	{"tool_costs", toolCostCases},
	{"no_bodies", noBodiesCases},
}

// Run runs the suite. newStore returns a migrated store on an empty database
// that is private to t; it must close and drop what it made when t ends, and
// skip t when no Postgres is available.
func Run(t *testing.T, newStore func(t *testing.T) *postgres.Store) {
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			for _, c := range g.cases {
				t.Run(c.name, func(t *testing.T) {
					c.run(t, &env{Store: newStore(t), gen: NewGen()})
				})
			}
		})
	}
}

// write writes the calls and fails the case on error.
func (e *env) write(t *testing.T, calls ...core.CallRecord) {
	t.Helper()
	if err := e.WriteBatch(context.Background(), calls); err != nil {
		t.Fatal(err)
	}
}

// counts returns the number of stored calls and tool halves.
func (e *env) counts(t *testing.T) (calls, halves int) {
	t.Helper()
	ctx := context.Background()
	calls, err := e.CountRows(ctx, "calls")
	if err != nil {
		t.Fatal(err)
	}
	halves, err = e.CountRows(ctx, "tool_events")
	if err != nil {
		t.Fatal(err)
	}
	return calls, halves
}

// wantCounts fails the case unless the store holds exactly these rows.
func (e *env) wantCounts(t *testing.T, calls, halves int) {
	t.Helper()
	c, h := e.counts(t)
	if c != calls || h != halves {
		t.Fatalf("stored %d calls and %d tool halves, want %d and %d", c, h, calls, halves)
	}
}
