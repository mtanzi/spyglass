import type { ResponseOf } from '../api/client'
import type { CostRange } from '../search/routes'
import { compare } from './dashboardData'
import type { Change, GroupTotal } from './dashboardData'

// Pure helpers of the cost section on the overview (ADR 0059). Numbers come
// from GET /admin/usage/months, /projects and /sessions. Unknown is not zero:
// a cost the API does not give is null and is never counted as zero.

export type MonthRow = ResponseOf<'/admin/usage/months'>['months'][number]
export type ProjectsResponse = ResponseOf<'/admin/usage/projects'>

const DAY_MS = 24 * 60 * 60 * 1000

/** The number of calendar months of each period, the current month included. */
export const COST_RANGE_MONTHS: Record<CostRange, number> = { '3m': 3, '6m': 6, '12m': 12 }

/** The IANA zone of the browser, which is the zone the section sends and shows. */
export function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

/**
 * The window of a period: the start of the local month N-1 months back, up to
 * now. `Date` works in the browser zone, which is the zone sent as `tz`, so
 * the server cuts the same months.
 */
export function costWindow(range: CostRange, now: Date): { from: string; to: string } {
  const from = new Date(now.getFullYear(), now.getMonth() - (COST_RANGE_MONTHS[range] - 1), 1)
  return { from: from.toISOString(), to: now.toISOString() }
}

/** A share of a whole as a percentage. A small share reads "<1%", never "0%". */
export function formatShare(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return ''
  const pct = value * 100
  if (pct > 0 && pct < 0.5) return '<1%'
  return `${Math.round(pct)}%`
}

/**
 * The known cost of the rows, null when no row has one. A row with no calls
 * is skipped: the API pads a month with no calls with a cost of 0, and that
 * zero must not make the cost of unpriced calls in the other months known.
 */
export function knownTotal(rows: { cost_micro_usd: number | null; calls?: number }[]): number | null {
  let known = false
  let sum = 0
  for (const r of rows) {
    if (r.cost_micro_usd === null || r.calls === 0) continue
    known = true
    sum += r.cost_micro_usd
  }
  return known ? sum : null
}

/** The share of `part` in `total`; null when either is unknown or the total is zero. */
export function shareOf(part: number | null, total: number | null): number | null {
  return part === null || total === null || total <= 0 ? null : part / total
}

/** A headline: nothing to show, calls with no known cost, or the leader with its share. */
export type Leader<T> =
  | { kind: 'none' }
  | { kind: 'unknown' }
  | { kind: 'known'; row: T; share: number | null; partial: boolean }

/**
 * The month with the highest known cost. A month with no calls is not a
 * candidate. A tie goes to the later month. A month with unpriced calls stays
 * a candidate and is marked partial: its cost is a floor.
 */
export function mostCostlyMonth(months: MonthRow[]): Leader<MonthRow> {
  const withCalls = months.filter((m) => m.calls > 0)
  if (withCalls.length === 0) return { kind: 'none' }
  let best: MonthRow | undefined
  for (const m of withCalls) {
    if (m.cost_micro_usd === null) continue
    if (best === undefined || m.cost_micro_usd >= (best.cost_micro_usd ?? 0)) best = m
  }
  if (best === undefined) return { kind: 'unknown' }
  return {
    kind: 'known',
    row: best,
    share: shareOf(best.cost_micro_usd, knownTotal(months)),
    partial: best.unpriced_calls > 0,
  }
}

/**
 * The first project of the ranking, which the API orders by known cost. The
 * share is of the cost of the whole period, which includes the projects past
 * the limit and the calls with no project; with no period cost there is no share.
 */
export function mostCostlyProject(
  data: ProjectsResponse,
  periodCost: number | null,
): Leader<ProjectsResponse['projects'][number]> {
  const first = data.projects[0]
  if (first === undefined) return { kind: 'none' }
  if (first.cost_micro_usd === null) return { kind: 'unknown' }
  return {
    kind: 'known',
    row: first,
    share: shareOf(first.cost_micro_usd, periodCost),
    partial: first.unpriced_calls > 0,
  }
}

/** The project rows as the rows of a ranked list. */
export function projectRows(data: ProjectsResponse): GroupTotal[] {
  return data.projects.map((p) => ({
    group: p.project,
    calls: p.calls,
    errors: 0,
    cost: p.cost_micro_usd,
    unpriced: p.unpriced_calls,
  }))
}

/** "Mar 2026" for the month "2026-03", in whatever zone: the name of a month does not move. */
export function monthLabel(month: string, style: 'short' | 'long' = 'short'): string {
  const [y, m] = month.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString(undefined, {
    month: style,
    year: style === 'long' ? 'numeric' : '2-digit',
    timeZone: 'UTC',
  })
}

export interface MonthBar {
  month: string
  /** Height as a share of the highest bar; null when the month has no known cost. */
  height: number | null
  empty: boolean
}

/** One bar per month. A month with no calls or no known cost draws no bar. */
export function monthBars(months: MonthRow[]): MonthBar[] {
  const max = Math.max(0, ...months.map((m) => m.cost_micro_usd ?? 0))
  return months.map((m) => ({
    month: m.month,
    empty: m.calls === 0,
    height: m.calls === 0 || m.cost_micro_usd === null ? null : max > 0 ? m.cost_micro_usd / max : 0,
  }))
}

export interface RunRate {
  /** The month, such as "2026-11". */
  month: string
  /** Known cost so far this month. */
  toDate: number | null
  unpriced: number
  /** The cost at the current pace; null before the month is a day old, or with no known cost. */
  projected: number | null
  /** The last full month, when the period holds one. */
  previous: { month: string; cost: number | null } | null
  /** The projection against the last full month. */
  change: Change | null
}

/**
 * The month so far and the cost at the current pace: a linear projection of
 * the known cost, so it ignores weekday patterns. Null when the last month of
 * the period is not the one that holds `now`.
 */
export function runRate(months: MonthRow[], now: Date): RunRate | null {
  const current = months.at(-1)
  if (current === undefined) return null
  const start = Date.parse(current.start)
  const end = Date.parse(current.end)
  const at = now.getTime()
  if (!(start <= at && at < end)) return null
  const elapsed = at - start
  const projected =
    current.cost_micro_usd !== null && elapsed >= DAY_MS
      ? Math.round((current.cost_micro_usd * (end - start)) / elapsed)
      : null
  const before = months.at(-2)
  const previous = before !== undefined && !before.partial && before.calls > 0 ? { month: before.month, cost: before.cost_micro_usd } : null
  return {
    month: current.month,
    toDate: current.cost_micro_usd,
    unpriced: current.unpriced_calls,
    projected,
    previous,
    change: previous ? compare(projected, previous.cost, 'relative') : null,
  }
}

export interface Averages {
  perSession: number | null
  perCall: number | null
  /** Calls with no cost in the period, left out of both averages. */
  unpriced: number
}

/**
 * Known cost per session and per priced call over the period. The sum covers
 * every call, so calls with no session key raise the per-session average.
 */
export function averages(months: MonthRow[], sessionCount: number): Averages {
  const cost = knownTotal(months)
  const calls = months.reduce((n, m) => n + m.calls, 0)
  const unpriced = months.reduce((n, m) => n + m.unpriced_calls, 0)
  const priced = calls - unpriced
  return {
    perSession: cost !== null && sessionCount > 0 ? Math.round(cost / sessionCount) : null,
    perCall: cost !== null && priced > 0 ? Math.round(cost / priced) : null,
    unpriced,
  }
}

/** The window of one month for a link: the month, cut at `now` when it is still running. */
export function monthWindow(month: MonthRow, now: Date): { from: string; to: string } {
  const end = Date.parse(month.end)
  return { from: month.start, to: end > now.getTime() ? now.toISOString() : month.end }
}
