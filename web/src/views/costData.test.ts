import { expect, test } from 'vitest'
import { formatCostMicroUsd } from '../lib/format'
import {
  MAX_SLOTS,
  chartRows,
  costGrain,
  costRows,
  costShare,
  knownCost,
  promptCoverage,
  sortByCost,
  taskRows,
  topSessions,
} from './costData'
import { timeSlots } from './dashboardData'
import { bucket } from './dashboardFixtures.test-util'

const day = { from: '2026-11-01T00:00:00.000Z', to: '2026-11-02T00:00:00.000Z' }

test('grouped buckets sum over time into one row per group', () => {
  const rows = costRows([
    bucket({ start: '2026-11-01T10:00:00Z', group: 'opus', calls: 4, cost_micro_usd: 4_000_000 }),
    bucket({ start: '2026-11-01T11:00:00Z', group: 'opus', calls: 6, cost_micro_usd: 6_000_000 }),
    bucket({ start: '2026-11-01T10:00:00Z', group: 'haiku', calls: 10, cost_micro_usd: 500_000 }),
  ])
  expect(rows.find((r) => r.group === 'opus')).toMatchObject({ calls: 10, cost: 10_000_000, unpriced: 0 })
  expect(rows.find((r) => r.group === 'haiku')).toMatchObject({ calls: 10, cost: 500_000 })
})

test('cache hit of a group is cache_read over all input columns, null without input', () => {
  const usage = { input_uncached: 60, cache_read: 40, cache_write_5m: 0, cache_write_1h: 0, output: 5, reasoning: 0 }
  const [row] = costRows([bucket({ group: 'a', usage }), bucket({ group: 'a', start: '2026-11-01T11:00:00Z', usage })])
  expect(row.cacheHit).toBeCloseTo(0.4)
  const none = { ...usage, input_uncached: 0, cache_read: 0 }
  expect(costRows([bucket({ group: 'b', usage: none })])[0].cacheHit).toBeNull()
})

test('a group whose calls are all unpriced has an unknown cost, not zero', () => {
  const [row] = costRows([bucket({ group: 'x', calls: 3, unpriced_calls: 3, cost_micro_usd: 0 })])
  expect(row.cost).toBeNull()
  expect(formatCostMicroUsd(row.cost)).toBe('unknown')
})

test('a partly unpriced group keeps its known cost and counts the unpriced calls', () => {
  const [row] = costRows([bucket({ group: 'x', calls: 4, unpriced_calls: 1, cost_micro_usd: 2_000_000 })])
  expect(row).toMatchObject({ cost: 2_000_000, unpriced: 1 })
})

test('ranking puts unknown cost after every known cost, then by calls', () => {
  const rows = sortByCost([
    { group: 'u1', calls: 50, cost: null },
    { group: 'cheap', calls: 1, cost: 10 },
    { group: 'u2', calls: 90, cost: null },
    { group: 'big', calls: 1, cost: 900 },
  ])
  expect(rows.map((r) => r.group)).toEqual(['big', 'cheap', 'u2', 'u1'])
})

test('share is a whole percent of the known cost; unknown has none', () => {
  const rows = costRows([
    bucket({ group: 'a', cost_micro_usd: 750 }),
    bucket({ group: 'b', cost_micro_usd: 250 }),
    bucket({ group: 'c', calls: 2, unpriced_calls: 2, cost_micro_usd: 0 }),
  ])
  const total = knownCost(rows)
  expect(total).toBe(1000)
  expect(costShare(750, total)).toBe(75)
  expect(costShare(null, total)).toBeNull()
  expect(costShare(0, 0)).toBeNull()
})

test('cost per firstmate task groups the fm: keys by their task id', () => {
  const rows = costRows([
    bucket({ group: 'fm:spyglass-adr-0035', cost_micro_usd: 3_000_000 }),
    bucket({ group: 'fm:other-task', cost_micro_usd: 9_000_000 }),
    bucket({ group: 'fm:', cost_micro_usd: 1 }),
    bucket({ group: 'laptop', cost_micro_usd: 99_000_000 }),
    bucket({ group: 'xfm:not-a-task', cost_micro_usd: 99_000_000 }),
  ])
  const tasks = taskRows(rows)
  expect(tasks.map((t) => t.taskId)).toEqual(['other-task', 'spyglass-adr-0035'])
  expect(tasks[0].group).toBe('fm:other-task')
})

test('top sessions drop calls without a session and keep the limit', () => {
  const rows = costRows([
    bucket({ group: '', cost_micro_usd: 99_000_000 }),
    bucket({ group: 's1', cost_micro_usd: 1 }),
    bucket({ group: 's2', cost_micro_usd: 3 }),
    bucket({ group: 's3', cost_micro_usd: 2 }),
  ])
  expect(topSessions(rows, 2).map((r) => r.group)).toEqual(['s2', 's3'])
})

test('the prompt table says so when the range has more prompts than it lists', () => {
  expect(promptCoverage(25, 25)).toBeUndefined()
  expect(promptCoverage(3, 3)).toBeUndefined()
  expect(promptCoverage(25, 1_030)).toBe('Showing the 25 most costly of 1,030 prompts; 1,005 more are not shown.')
})

test('the grain follows the length of the window and never gives too many points', () => {
  expect(costGrain({ from: '2026-11-01T00:00:00.000Z', to: '2026-11-01T01:00:00.000Z' })).toBe('minute')
  expect(costGrain(day)).toBe('hour')
  expect(costGrain({ from: '2026-10-01T00:00:00.000Z', to: '2026-11-01T00:00:00.000Z' })).toBe('day')
  // A requested grain is kept while it stays within the limit.
  expect(costGrain(day, 'day')).toBe('day')
  expect(costGrain(day, 'minute')).toBe('hour')
  const month = { from: '2026-10-01T00:00:00.000Z', to: '2026-11-01T00:00:00.000Z' }
  expect(timeSlots(month, costGrain(month, 'minute')).length).toBeLessThanOrEqual(MAX_SLOTS)
  expect(costGrain(month, 'minute')).toBe('day')
})

test('chart table rows skip intervals without a value', () => {
  const slots = [0, 1, 2, 3]
  expect(
    chartRows(slots, [
      { values: [0, 5, 0, 0] },
      { values: [0, 0, null, 2] },
    ]),
  ).toEqual([
    { start: 1, values: [5, 0] },
    { start: 3, values: [0, 2] },
  ])
  expect(chartRows(slots, [{ values: [null, null, null, null] }])).toEqual([])
})

test('chart table rows of a ratio keep a 0% interval and skip only missing values', () => {
  expect(chartRows([0, 1, 2, 3], [{ values: [0, 0.4, null, 0] }], true)).toEqual([
    { start: 0, values: [0] },
    { start: 1, values: [0.4] },
    { start: 3, values: [0] },
  ])
  expect(chartRows([0, 1], [{ values: [0, 0] }], true)).toHaveLength(2)
})

test('a 90-day window at minute grain falls back to day', () => {
  const quarter = { from: '2026-08-03T00:00:00.000Z', to: '2026-11-01T00:00:00.000Z' }
  expect(costGrain(quarter, 'minute')).toBe('day')
  // 400 hours is the limit: an hour grain is kept up to there and no further.
  expect(costGrain({ from: '2026-11-01T00:00:00.000Z', to: '2026-11-17T16:00:00.000Z' }, 'hour')).toBe('hour')
  expect(costGrain({ from: '2026-11-01T00:00:00.000Z', to: '2026-11-17T16:00:01.000Z' }, 'hour')).toBe('day')
})
