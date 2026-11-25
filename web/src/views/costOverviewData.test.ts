import { expect, test } from 'vitest'
import {
  allCostsUnknown,
  averages,
  cellName,
  cellsByCost,
  costWindow,
  formatShare,
  knownTotal,
  markerRadius,
  monthBars,
  monthLabel,
  monthWindow,
  mostCostlyCell,
  mostCostlyMonth,
  mostCostlyProject,
  runRate,
  weekdayName,
} from './costOverviewData'
import type { MonthRow, ProjectsResponse, PunchcardCell } from './costOverviewData'

function month(m: string, over: Partial<MonthRow> = {}): MonthRow {
  const [y, mo] = m.split('-').map(Number)
  return {
    month: m,
    start: new Date(Date.UTC(y, mo - 1, 1)).toISOString(),
    end: new Date(Date.UTC(y, mo, 1)).toISOString(),
    calls: 10,
    cost_micro_usd: 1_000_000,
    unpriced_calls: 0,
    api_equivalent: false,
    partial: false,
    ...over,
  }
}

const local = (iso: string) => {
  const d = new Date(iso)
  return [d.getFullYear(), d.getMonth() + 1, d.getDate(), d.getHours(), d.getMinutes()]
}

test('a period starts on the first day of a local month, N-1 months back', () => {
  const now = new Date(2026, 10, 15, 10, 30) // 15 Nov 2026, local
  const w3 = costWindow('3m', now)
  expect(local(w3.from)).toEqual([2026, 9, 1, 0, 0])
  expect(w3.to).toBe(now.toISOString())
  expect(local(costWindow('6m', now).from)).toEqual([2026, 6, 1, 0, 0])
  // 12 months reach back into the previous year; a short period near January too.
  expect(local(costWindow('12m', now).from)).toEqual([2025, 12, 1, 0, 0])
  expect(local(costWindow('3m', new Date(2026, 0, 5)).from)).toEqual([2025, 11, 1, 0, 0])
})

test('a share reads as a whole percent, and a small one never as zero', () => {
  expect(formatShare(1 / 3)).toBe('33%')
  expect(formatShare(0.5)).toBe('50%')
  expect(formatShare(0.004)).toBe('<1%')
  expect(formatShare(0)).toBe('0%')
  expect(formatShare(null)).toBe('')
})

test('the most costly month is the highest known cost, and a tie goes to the later month', () => {
  const months = [
    month('2026-09', { cost_micro_usd: 3_000_000 }),
    month('2026-10', { cost_micro_usd: 6_000_000 }),
    month('2026-11', { cost_micro_usd: 1_000_000, partial: true }),
  ]
  expect(mostCostlyMonth(months)).toEqual({ kind: 'known', row: months[1], share: 0.6, partial: false })
  const tie = [month('2026-09', { cost_micro_usd: 5 }), month('2026-10', { cost_micro_usd: 5 })]
  expect(mostCostlyMonth(tie)).toMatchObject({ kind: 'known', row: { month: '2026-10' }, share: 0.5 })
})

test('a month with unpriced calls can lead and is marked partial', () => {
  const months = [month('2026-10', { cost_micro_usd: 9_000_000, unpriced_calls: 2 }), month('2026-11', { cost_micro_usd: 1_000_000 })]
  expect(mostCostlyMonth(months)).toMatchObject({ kind: 'known', row: { month: '2026-10' }, partial: true })
})

test('unknown months never win, and an all-unknown or empty period has no leader', () => {
  const unknown = month('2026-11', { cost_micro_usd: null, unpriced_calls: 10 })
  expect(mostCostlyMonth([month('2026-10', { cost_micro_usd: 100 }), unknown])).toMatchObject({ kind: 'known', row: { month: '2026-10' }, share: 1 })
  expect(mostCostlyMonth([unknown, month('2026-10', { cost_micro_usd: null, unpriced_calls: 10 })])).toEqual({ kind: 'unknown' })
  // A month with no calls has cost 0 and is not a candidate.
  expect(mostCostlyMonth([month('2026-10', { calls: 0, cost_micro_usd: 0 })])).toEqual({ kind: 'none' })
  expect(mostCostlyMonth([])).toEqual({ kind: 'none' })
})

function projects(over: Partial<ProjectsResponse> = {}): ProjectsResponse {
  const row = (project: string, cost: number | null, unpriced = 0) => ({
    project,
    calls: 5,
    sessions: 1,
    cost_micro_usd: cost,
    unpriced_calls: unpriced,
    api_equivalent: false,
  })
  return {
    projects: [row('alpha', 4_000_000, 1), row('beta', 1_000_000)],
    project_count: 2,
    unassigned: { calls: 0, cost_micro_usd: 0, unpriced_calls: 0 },
    ...over,
  }
}

test('the most costly project is the first of the ranking, as a share of the whole period', () => {
  // The period cost counts projects past the list and calls with no project: 4 of 16.
  expect(mostCostlyProject(projects(), 16_000_000)).toMatchObject({ kind: 'known', row: { project: 'alpha' }, share: 0.25, partial: true })
  // Without a period cost there is no share: the listed projects are not the whole period.
  expect(mostCostlyProject(projects(), null)).toMatchObject({ kind: 'known', row: { project: 'alpha' }, share: null })
  expect(mostCostlyProject(projects({ projects: [], project_count: 0 }), 5)).toEqual({ kind: 'none' })
  const all = projects()
  all.projects = all.projects.map((p) => ({ ...p, cost_micro_usd: null }))
  expect(mostCostlyProject(all, null)).toEqual({ kind: 'unknown' })
})

test('a bar is as high as its cost is to the highest, and unknown or empty months draw none', () => {
  const bars = monthBars([
    month('2026-08', { cost_micro_usd: 2_000_000 }),
    month('2026-09', { cost_micro_usd: 4_000_000 }),
    month('2026-10', { cost_micro_usd: null, unpriced_calls: 10 }),
    month('2026-11', { calls: 0, cost_micro_usd: 0 }),
  ])
  expect(bars.map((b) => b.height)).toEqual([0.5, 1, null, null])
  expect(bars.map((b) => b.empty)).toEqual([false, false, false, true])
})

test('a month is named by its calendar month, whatever the zone of the browser', () => {
  expect(monthLabel('2026-03', 'long')).toBe('March 2026')
  expect(monthLabel('2026-12')).toContain('Dec')
})

test('the run-rate projects the known cost of the month so far over the whole month', () => {
  // 14.5 days of November (30 days) have passed.
  const now = new Date(Date.UTC(2026, 10, 15, 12))
  const months = [month('2026-10', { cost_micro_usd: 40_000_000 }), month('2026-11', { cost_micro_usd: 29_000_000, partial: true })]
  const rate = runRate(months, now)
  expect(rate).toMatchObject({ month: '2026-11', toDate: 29_000_000, projected: 60_000_000, previous: { month: '2026-10', cost: 40_000_000 } })
  expect(rate?.change).toEqual({ kind: 'up', value: 0.5 })
})

test('the run-rate waits a day, skips a cut previous month and needs the current month', () => {
  const early = new Date(Date.UTC(2026, 10, 1, 6))
  expect(runRate([month('2026-11', { cost_micro_usd: 1_000_000 })], early)).toMatchObject({ toDate: 1_000_000, projected: null })
  const now = new Date(Date.UTC(2026, 10, 15, 12))
  // A previous month the range cut is not a full month to compare with.
  const cut = [month('2026-10', { cost_micro_usd: 40_000_000, partial: true }), month('2026-11', { cost_micro_usd: 1_000_000 })]
  expect(runRate(cut, now)).toMatchObject({ previous: null, change: null })
  // The last month of the response is not the month of `now`.
  expect(runRate([month('2026-09'), month('2026-10')], now)).toBeNull()
  expect(runRate([], now)).toBeNull()
  // No known cost: unknown is not zero, so there is nothing to project.
  expect(runRate([month('2026-11', { cost_micro_usd: null, unpriced_calls: 10 })], now)).toMatchObject({ toDate: null, projected: null })
})

test('averages divide the known cost by the sessions and by the priced calls', () => {
  const months = [month('2026-10', { calls: 60, cost_micro_usd: 6_000_000 }), month('2026-11', { calls: 40, unpriced_calls: 20, cost_micro_usd: 4_000_000 })]
  // 10 USD over 4 sessions and over the 80 priced calls of 100.
  expect(averages(months, 4)).toEqual({ perSession: 2_500_000, perCall: 125_000, unpriced: 20 })
  expect(averages(months, 0).perSession).toBeNull()
  const unknown = [month('2026-11', { calls: 5, cost_micro_usd: null, unpriced_calls: 5 })]
  expect(averages(unknown, 2)).toEqual({ perSession: null, perCall: null, unpriced: 5 })
  // The API pads a month with no calls with a cost of 0 (GET /admin/usage/months).
  // That zero is not a known cost of the unpriced calls: the averages stay unknown.
  const padded = [month('2026-10', { calls: 0, cost_micro_usd: 0 }), ...unknown]
  expect(averages(padded, 2)).toEqual({ perSession: null, perCall: null, unpriced: 5 })
  expect(knownTotal(padded)).toBeNull()
})

test('the window of a month for a link stops at now while the month runs', () => {
  const now = new Date(Date.UTC(2026, 10, 15, 12))
  const past = month('2026-10')
  expect(monthWindow(past, now)).toEqual({ from: past.start, to: past.end })
  const running = month('2026-11')
  expect(monthWindow(running, now)).toEqual({ from: running.start, to: now.toISOString() })
})

function cell(weekday: number, hour: number, cost: number | null, over: Partial<PunchcardCell> = {}): PunchcardCell {
  return { weekday, hour, calls: 5, cost_micro_usd: cost, unpriced_calls: 0, api_equivalent: false, ...over }
}

test('a marker has an area proportional to its cost, and unknown is not zero', () => {
  // Four times the cost, twice the radius; the highest cost gets the full radius.
  expect(markerRadius(400, 400, 10)).toBe(10)
  expect(markerRadius(100, 400, 10)).toBe(5)
  expect(markerRadius(25, 400, 10)).toBe(2.5)
  expect(markerRadius(0, 400, 10)).toBe(0)
  expect(markerRadius(null, 400, 10)).toBeNull()
  // No cost anywhere: nothing to scale against.
  expect(markerRadius(0, 0, 10)).toBe(0)
})

test('the most costly hour is the highest known cost, a tie goes to the earlier cell, and unknown never wins', () => {
  const cells = [cell(1, 9, 100), cell(2, 14, 300), cell(5, 3, 300), cell(6, 22, null, { unpriced_calls: 5 })]
  expect(mostCostlyCell(cells)).toEqual({ kind: 'known', row: cells[1], share: 300 / 700, partial: false })
  expect(mostCostlyCell([cell(1, 9, 100, { unpriced_calls: 2 })])).toMatchObject({ kind: 'known', partial: true, share: 1 })
  expect(mostCostlyCell([cell(6, 22, null), cell(7, 1, null)])).toEqual({ kind: 'unknown' })
  expect(mostCostlyCell([])).toEqual({ kind: 'none' })
})

test('the table fallback of the punchcard ranks by known cost, then calls, then the earlier cell, with unknown last', () => {
  const cells = [
    cell(7, 0, null, { calls: 99 }),
    cell(3, 10, 100, { calls: 2 }),
    cell(2, 10, 100, { calls: 9 }),
    cell(4, 8, 300),
    cell(2, 9, 100, { calls: 9 }),
  ]
  expect(cellsByCost(cells).map(cellName)).toEqual([
    'Thursday 08:00–08:59',
    'Tuesday 09:00–09:59',
    'Tuesday 10:00–10:59',
    'Wednesday 10:00–10:59',
    'Sunday 00:00–00:59',
  ])
})

test('weekdays run from Monday as 1 to Sunday as 7', () => {
  expect([1, 4, 7].map(weekdayName)).toEqual(['Monday', 'Thursday', 'Sunday'])
})

test('a ranked list is all unknown only when it has rows and none has a cost', () => {
  expect(allCostsUnknown([])).toBe(false)
  expect(allCostsUnknown([{ cost_micro_usd: null }, { cost_micro_usd: null }])).toBe(true)
  expect(allCostsUnknown([{ cost_micro_usd: null }, { cost_micro_usd: 0 }])).toBe(false)
})
