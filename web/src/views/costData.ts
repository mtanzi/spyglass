import type { ResponseOf } from '../api/client'
import { formatCount } from '../lib/format'
import type { CostGroup } from '../search/routes'
import { inputTokens, slotCount } from './dashboardData'
import type { Grain, UsageBucket, Window } from './dashboardData'

// Pure helpers of the cost and cache view (ADR 0035). Numbers come from
// GET /admin/usage and GET /admin/usage/prompts. Unknown is not zero: a cost the API
// does not give is null and shows as "unknown".

export const GROUP_LABELS: Record<CostGroup, string> = {
  model: 'model',
  key: 'key',
  client: 'client',
  session: 'session',
}

/** The key-name prefix of a firstmate task key: `fm:<task-id>`. */
export const TASK_KEY_PREFIX = 'fm:'

/** The largest number of points one chart draws. A finer grain than this is coarsened. */
export const MAX_SLOTS = 400

const GRAIN_ORDER: Grain[] = ['minute', 'hour', 'day']

/**
 * The bucket width for a window. A requested grain wins unless it would give
 * more than MAX_SLOTS points, then the next coarser one is used. Without a
 * request: minutes up to 6 hours, hours up to 7 days, days beyond.
 */
export function costGrain(window: Window, requested?: Grain): Grain {
  const span = Date.parse(window.to) - Date.parse(window.from)
  let grain: Grain = requested ?? (span <= 6 * 3_600_000 ? 'minute' : span <= 7 * 86_400_000 ? 'hour' : 'day')
  while (grain !== 'day' && slotCount(window, grain) > MAX_SLOTS) {
    grain = GRAIN_ORDER[GRAIN_ORDER.indexOf(grain) + 1]
  }
  return grain
}

export interface CostRow {
  /** The group value; empty when not known. */
  group: string
  calls: number
  /** Known cost; null when every call is unpriced. */
  cost: number | null
  unpriced: number
  /** cache_read over all input columns; null without input tokens. */
  cacheHit: number | null
}

/** Sums a grouped series over time, one row per group, with the cache hit ratio of the group. */
export function costRows(buckets: UsageBucket[]): CostRow[] {
  const by = new Map<string, { calls: number; cost: number; unpriced: number; input: number; cacheRead: number }>()
  for (const b of buckets) {
    const row = by.get(b.group) ?? { calls: 0, cost: 0, unpriced: 0, input: 0, cacheRead: 0 }
    row.calls += b.calls
    row.cost += b.cost_micro_usd
    row.unpriced += b.unpriced_calls
    row.input += inputTokens(b.usage)
    row.cacheRead += b.usage.cache_read
    by.set(b.group, row)
  }
  return [...by.entries()].map(([group, r]) => ({
    group,
    calls: r.calls,
    // Every call unpriced: the cost is unknown, not zero.
    cost: r.calls > 0 && r.unpriced === r.calls ? null : r.cost,
    unpriced: r.unpriced,
    cacheHit: r.input > 0 ? r.cacheRead / r.input : null,
  }))
}

/** Highest known cost first; unknown cost after every known one; then by calls and name. */
export function sortByCost<T extends { group: string; calls: number; cost: number | null }>(rows: T[]): T[] {
  return [...rows].sort((a, b) => {
    if (a.cost === null || b.cost === null) {
      if (a.cost !== b.cost) return a.cost === null ? 1 : -1
    } else if (a.cost !== b.cost) {
      return b.cost - a.cost
    }
    return b.calls - a.calls || a.group.localeCompare(b.group)
  })
}

/** The known cost of the rows: the base of the share column. */
export function knownCost(rows: { cost: number | null }[]): number {
  return rows.reduce((sum, r) => sum + (r.cost ?? 0), 0)
}

/** A row's share of the known cost, as a whole percent; null for an unknown cost or no known cost. */
export function costShare(cost: number | null, total: number): number | null {
  return cost === null || total <= 0 ? null : Math.round((cost / total) * 100)
}

export interface TaskRow extends CostRow {
  /** The firstmate task id: the key name without its `fm:` prefix. */
  taskId: string
}

/**
 * The groups with a known cost in a series filtered to client_passthrough
 * providers: part of their cost is API-equivalent.
 */
export function apiEquivalentGroups(passthroughBuckets: UsageBucket[]): Set<string> {
  return new Set(passthroughBuckets.filter((b) => b.cost_micro_usd > 0).map((b) => b.group))
}

/** Cost per firstmate task: the rows of a series grouped by key whose name starts with `fm:`. */
export function taskRows(rows: CostRow[]): TaskRow[] {
  return sortByCost(
    rows
      .filter((r) => r.group.startsWith(TASK_KEY_PREFIX) && r.group.length > TASK_KEY_PREFIX.length)
      .map((r) => ({ ...r, taskId: r.group.slice(TASK_KEY_PREFIX.length) })),
  )
}

/** Sessions ranked by cost. Calls without a session (group "") are not a session. */
export function topSessions(rows: CostRow[], limit: number): CostRow[] {
  return sortByCost(rows.filter((r) => r.group !== '')).slice(0, limit)
}

/** One prompt of GET /admin/usage/prompts: its calls, known cost and unpriced count. */
export type PromptCost = ResponseOf<'/admin/usage/prompts'>['prompts'][number]

/**
 * The line under the prompt table. The server lists the most costly prompts
 * only, so when the range has more, say how many are not shown instead of
 * implying the table covers the range.
 */
export function promptCoverage(shown: number, total: number): string | undefined {
  if (total <= shown) return undefined
  return `Showing the ${formatCount(shown)} most costly of ${formatCount(total)} prompts; ${formatCount(total - shown)} more are not shown.`
}

/** One row of a chart's table fallback: the interval and one value per series. */
export interface ChartRow {
  start: number
  values: (number | null)[]
}

/**
 * Rows for the intervals with at least one value above zero, so an empty range
 * is not a wall of zeros. With keepZero, a zero is a value too (a ratio of 0%
 * is data), and only intervals without any value are left out.
 */
export function chartRows(slots: number[], series: { values: (number | null)[] }[], keepZero = false): ChartRow[] {
  return slots
    .map((start, i) => ({ start, values: series.map((s) => s.values[i] ?? null) }))
    .filter((r) => r.values.some((v) => v !== null && (keepZero || v > 0)))
}
