import { expect, test } from 'vitest'
import { validateDashboardSearch } from '../search/routes'
import {
  apiEquivalentCost,
  compare,
  errorGroups,
  formatChange,
  groupTotals,
  passthroughProviders,
  previousWindow,
  problemSearch,
  rankByCost,
  slotSeries,
  stackByGroup,
  timeSlots,
  totals,
} from './dashboardData'
import { bucket } from './dashboardFixtures.test-util'

test('the previous period has the same length and ends where the window starts', () => {
  expect(previousWindow({ from: '2026-11-01T00:00:00.000Z', to: '2026-11-02T00:00:00.000Z' })).toEqual({
    from: '2026-10-31T00:00:00.000Z',
    to: '2026-11-01T00:00:00.000Z',
  })
  expect(previousWindow({ from: '2026-11-01T11:30:00.000Z', to: '2026-11-01T12:30:00.000Z' })).toEqual({
    from: '2026-11-01T10:30:00.000Z',
    to: '2026-11-01T11:30:00.000Z',
  })
})

test('a relative change compares against the previous value', () => {
  expect(compare(150, 100, 'relative')).toEqual({ kind: 'up', value: 0.5 })
  expect(compare(75, 100, 'relative')).toEqual({ kind: 'down', value: 0.25 })
  expect(compare(100, 100, 'relative')).toEqual({ kind: 'same' })
  expect(compare(0, 0, 'relative')).toEqual({ kind: 'same' })
  // Nothing before: the change is "new", not an infinite percentage.
  expect(compare(5, 0, 'relative')).toEqual({ kind: 'new' })
  // Unknown on either side is unknown, never treated as zero.
  expect(compare(null, 100, 'relative')).toEqual({ kind: 'unknown' })
  expect(compare(100, null, 'relative')).toEqual({ kind: 'unknown' })
})

test('a ratio change is a difference in points', () => {
  const change = compare(0.12, 0.1, 'points')
  expect(change.kind).toBe('up')
  expect(formatChange(change, 'points')).toBe('+2.0 pts')
  expect(formatChange(compare(0.1, 0.15, 'points'), 'points')).toBe('−5.0 pts')
  expect(compare(0, 0, 'points')).toEqual({ kind: 'same' })
})

test('changes format with a sign, and words for the special cases', () => {
  expect(formatChange({ kind: 'up', value: 0.5 }, 'relative')).toBe('+50%')
  expect(formatChange({ kind: 'down', value: 0.034 }, 'relative')).toBe('−3.4%')
  expect(formatChange({ kind: 'same' }, 'relative')).toBe('no change')
  expect(formatChange({ kind: 'new' }, 'relative')).toBe('new')
  expect(formatChange({ kind: 'unknown' }, 'relative')).toBe('unknown')
})

test('totals sum the buckets; cost is unknown when every call is unpriced', () => {
  const t = totals([
    bucket(),
    bucket({ start: '2026-11-01T11:00:00Z', calls: 30, errors: 0, latency_p50_ms: 200, latency_p95_ms: 800 }),
  ])
  expect(t.calls).toBe(40)
  expect(t.errors).toBe(1)
  expect(t.tokensIn).toBe(200)
  expect(t.tokensOut).toBe(40)
  expect(t.cost).toBe(2_000)
  expect(t.errorRate).toBe(1 / 40)
  expect(t.cacheHit).toBe(80 / 200)
  // Call-weighted: (100*10 + 200*30) / 40.
  expect(t.p50).toBe(175)
  expect(t.p95).toBe(700)
  expect(t.latencyExact).toBe(false)

  const unpriced = totals([bucket({ unpriced_calls: 10, cost_micro_usd: 0 })])
  expect(unpriced.cost).toBeNull()
  expect(unpriced.latencyExact).toBe(true)

  const empty = totals([])
  expect(empty.cost).toBe(0)
  expect(empty.errorRate).toBeNull()
  expect(empty.cacheHit).toBeNull()
  expect(empty.p50).toBeNull()
})

test('slot series fill empty slots: zero for sums, null for ratios', () => {
  const slots = timeSlots({ from: '2026-11-01T09:30:00Z', to: '2026-11-01T12:00:00Z' }, 'hour')
  expect(slots.map((t) => new Date(t).toISOString())).toEqual([
    '2026-11-01T09:00:00.000Z',
    '2026-11-01T10:00:00.000Z',
    '2026-11-01T11:00:00.000Z',
  ])
  const buckets = [bucket()]
  expect(slotSeries(buckets, slots, 'calls')).toEqual([0, 10, 0])
  expect(slotSeries(buckets, slots, 'errorRate')).toEqual([null, 0.1, null])
  expect(slotSeries(buckets, slots, 'p95')).toEqual([null, 400, null])
  expect(slotSeries([bucket({ unpriced_calls: 10 })], slots, 'cost')).toEqual([0, null, 0])
})

test('errors group by class, most first, without the calls that have no error class', () => {
  const problems = errorGroups([
    bucket({ group: '', calls: 50, errors: 0 }),
    bucket({ group: 'rate_limited', calls: 3, errors: 3 }),
    bucket({ group: 'upstream_5xx', calls: 5, errors: 5 }),
    bucket({ start: '2026-11-02T00:00:00Z', group: 'rate_limited', calls: 4, errors: 4 }),
    bucket({ group: 'something_new', calls: 1, errors: 1 }),
  ])
  expect(problems).toEqual([
    { errorClass: 'rate_limited', label: 'Rate limited (429)', calls: 7 },
    { errorClass: 'upstream_5xx', label: 'Upstream 5xx (500, 529 overloaded)', calls: 5 },
    // An unknown class keeps its own name.
    { errorClass: 'something_new', label: 'something_new', calls: 1 },
  ])
  expect(errorGroups([bucket({ group: '', errors: 0 })])).toEqual([])
})

test('a problem links to the call log filtered to its class, in the same range', () => {
  expect(problemSearch('rate_limited', '7d')).toEqual({ error_class: ['rate_limited'], range: '7d' })
  expect(problemSearch('timeout', undefined)).toEqual({ error_class: ['timeout'], range: undefined })
})

test('groups rank by cost, unknown cost last', () => {
  const rows = groupTotals([
    bucket({ group: 'a', cost_micro_usd: 500 }),
    bucket({ group: 'b', cost_micro_usd: 900 }),
    bucket({ group: 'c', cost_micro_usd: 0, unpriced_calls: 10 }),
    bucket({ start: '2026-11-02T00:00:00Z', group: 'a', cost_micro_usd: 600 }),
  ])
  expect(rankByCost(rows).map((r) => [r.group, r.cost])).toEqual([
    ['a', 1_100],
    ['b', 900],
    ['c', null],
  ])
  expect(rankByCost(rows, 1).map((r) => r.group)).toEqual(['a'])
})

test('the activity stack keeps the top models and folds the rest into other', () => {
  const slots = [Date.parse('2026-11-01T10:00:00Z'), Date.parse('2026-11-01T11:00:00Z')]
  const buckets = ['m1', 'm2', 'm3'].map((group, i) => bucket({ group, calls: 10 - i, errors: 1 }))
  buckets.push(bucket({ start: '2026-11-01T11:00:00Z', group: 'm3', calls: 2, errors: 0 }))
  const { series, errors } = stackByGroup(buckets, slots, 'calls', 2)
  expect(series).toEqual([
    { name: 'm1', values: [10, 0] },
    { name: 'other', values: [17, 2], other: true },
  ])
  expect(errors).toEqual([3, 0])
  // m3 has 10 calls over both slots, m2 has 9.
  expect(stackByGroup(buckets, slots, 'calls', 3).series.map((s) => s.name)).toEqual(['m1', 'm3', 'm2'])
})

test('passthrough cost is the API-equivalent part', () => {
  const passthrough = passthroughProviders([
    { name: 'anthropic', kind: 'anthropic', base_url: '', auth_mode: 'proxy_key', idle_timeout: '10m' },
    { name: 'max', kind: 'anthropic', base_url: '', auth_mode: 'client_passthrough', idle_timeout: '10m' },
  ])
  expect([...passthrough]).toEqual(['max'])
  expect(
    apiEquivalentCost([bucket({ group: 'anthropic', cost_micro_usd: 5 }), bucket({ group: 'max', cost_micro_usd: 7 })], passthrough),
  ).toBe(7)
})

test('the dashboard search keeps a preset and the cost metric, defaults stay out', () => {
  expect(validateDashboardSearch({ range: '7d', metric: 'cost' })).toEqual({ range: '7d', metric: 'cost' })
  expect(validateDashboardSearch({ range: '24h', metric: 'calls' })).toEqual({})
  // 15m is a call-log preset, not a dashboard one.
  expect(validateDashboardSearch({ range: '15m', from: '2026-11-01T00:00:00Z', metric: 'x' })).toEqual({})
  // The cost period is its own parameter; its default and unknown values stay out of the URL.
  expect(validateDashboardSearch({ cost_range: '12m' })).toEqual({ cost_range: '12m' })
  expect(validateDashboardSearch({ cost_range: '6m', range: '7d' })).toEqual({ cost_range: '6m', range: '7d' })
  expect(validateDashboardSearch({ cost_range: '3m' })).toEqual({})
  expect(validateDashboardSearch({ cost_range: '24m' })).toEqual({})
})
