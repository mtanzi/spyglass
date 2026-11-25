import { DEFAULT_RANGE, validateTimeRange } from './timeRange'
import type { TimeRangeSearch } from './timeRange'
import {
  asRaw,
  bool,
  compact,
  cursor,
  enumList,
  int,
  oneOf,
  statusList,
  textList,
} from './validate'

// Search parameters per route. Names match the API query parameters, so a
// view passes the validated search to the client with few changes. Views add
// their own parameters in later ADRs; the validators here are the shared part.

/** Call-log criteria (ADR 0031): the filters of GET /admin/calls. */
export interface CallFilterSearch {
  key?: string[]
  client?: string[]
  provider?: string[]
  model?: string[]
  endpoint?: string[]
  status?: number[]
  error_class?: string[]
  session?: string[]
  agent?: string[]
  agent_type?: string[]
  prompt?: string[]
  request_class?: string[]
  project?: string[]
  tool?: string[]
  streamed?: boolean
  tool_error?: boolean
  latency_ms_gte?: number
  cost_micro_usd_gte?: number
}

const CALL_TEXT_FILTERS = [
  'key',
  'client',
  'provider',
  'model',
  'endpoint',
  'error_class',
  'session',
  'agent',
  'agent_type',
  'prompt',
  'request_class',
  'project',
  'tool',
] as const

export function validateCallFilters(search: unknown): CallFilterSearch {
  const raw = asRaw(search)
  const out: CallFilterSearch = compact({
    status: statusList(raw.status),
    streamed: bool(raw.streamed),
    tool_error: bool(raw.tool_error),
    latency_ms_gte: int(raw.latency_ms_gte),
    cost_micro_usd_gte: int(raw.cost_micro_usd_gte),
  })
  for (const name of CALL_TEXT_FILTERS) {
    const list = textList(raw[name])
    if (list) out[name] = list
  }
  return out
}

export interface CallsSearch extends TimeRangeSearch, CallFilterSearch {
  cursor?: string
  group?: 'session'
  live?: boolean
}

export function validateCallsSearch(search: unknown): CallsSearch {
  const raw = asRaw(search)
  return compact({
    ...validateTimeRange(raw),
    ...validateCallFilters(raw),
    cursor: cursor(raw.cursor),
    group: oneOf(raw.group, ['session'] as const),
    // Off is the default: only an explicit true is kept.
    live: bool(raw.live) === true ? true : undefined,
  })
}

export const CALL_TABS = ['overview', 'tools', 'usage'] as const
export type CallTab = (typeof CALL_TABS)[number]
export interface CallDetailSearch {
  tab?: CallTab
}
export function validateCallDetailSearch(search: unknown): CallDetailSearch {
  const tab = oneOf(asRaw(search).tab, CALL_TABS)
  return tab === undefined || tab === 'overview' ? {} : { tab }
}

export interface SessionsSearch extends TimeRangeSearch {
  key?: string[]
  client?: string[]
  provider?: string[]
  model?: string[]
  error_class?: string[]
  cost_micro_usd_gte?: number
  cursor?: string
}
export function validateSessionsSearch(search: unknown): SessionsSearch {
  const raw = asRaw(search)
  return compact({
    ...validateTimeRange(raw),
    key: textList(raw.key),
    client: textList(raw.client),
    provider: textList(raw.provider),
    model: textList(raw.model),
    error_class: textList(raw.error_class),
    cost_micro_usd_gte: int(raw.cost_micro_usd_gte),
    cursor: cursor(raw.cursor),
  })
}

export const SESSION_MODES = ['tree', 'time'] as const
export type SessionMode = (typeof SESSION_MODES)[number]
export interface SessionDetailSearch {
  mode?: SessionMode
}
export function validateSessionDetailSearch(search: unknown): SessionDetailSearch {
  const mode = oneOf(asRaw(search).mode, SESSION_MODES)
  return mode === undefined || mode === 'tree' ? {} : { mode }
}

/** The unmatched halves: a use with no result after an hour, and a result with no use. */
export const UNMATCHED = ['use_only', 'result_only'] as const
/** Columns the tool summary sorts by (ADR 0034). Text columns sort ascending first, numbers descending. */
export const TOOL_SUMMARY_SORTS = ['name', 'agent_type', 'count', 'error_rate', 'p50', 'p95', 'total'] as const
export type ToolSummarySort = (typeof TOOL_SUMMARY_SORTS)[number]
export const SORT_ORDERS = ['asc', 'desc'] as const
export type SortOrder = (typeof SORT_ORDERS)[number]
/** The summary's default: the most-used tools first. */
export const DEFAULT_TOOL_SORT: ToolSummarySort = 'count'
export function defaultSortOrder(sort: ToolSummarySort): SortOrder {
  return sort === 'name' || sort === 'agent_type' ? 'asc' : 'desc'
}
export interface ToolsSearch extends TimeRangeSearch {
  session?: string
  tool?: string[]
  /** The "Unmatched only" filter: both unmatched halves or nothing. */
  status?: (typeof UNMATCHED)[number][]
  is_error?: boolean
  duration_ms_gte?: number
  arg_bytes_gte?: number
  result_bytes_gte?: number
  cursor?: string
  /** Sort column of the summary table. */
  sort?: ToolSummarySort
  order?: SortOrder
}
export function validateToolsSearch(search: unknown): ToolsSearch {
  const raw = asRaw(search)
  // Defaults stay out of the URL: count, and each column's own first direction.
  const sort = oneOf(raw.sort, TOOL_SUMMARY_SORTS) ?? DEFAULT_TOOL_SORT
  const orderRaw = oneOf(raw.order, SORT_ORDERS)
  const order = orderRaw === defaultSortOrder(sort) ? undefined : orderRaw
  return compact({
    sort: sort === DEFAULT_TOOL_SORT ? undefined : sort,
    order,
    ...validateTimeRange(raw),
    session: textList(raw.session)?.[0],
    tool: textList(raw.tool),
    status: enumList(raw.status, UNMATCHED)?.length === UNMATCHED.length ? [...UNMATCHED] : undefined,
    is_error: bool(raw.is_error),
    duration_ms_gte: int(raw.duration_ms_gte),
    arg_bytes_gte: int(raw.arg_bytes_gte),
    result_bytes_gte: int(raw.result_bytes_gte),
    cursor: cursor(raw.cursor),
  })
}

/** The groupings of the cost view (ADR 0035). */
export const COST_GROUPS = ['model', 'key', 'client', 'session'] as const
export type CostGroup = (typeof COST_GROUPS)[number]
export const DEFAULT_COST_GROUP: CostGroup = 'model'
export const GRAINS = ['minute', 'hour', 'day'] as const
export interface CostSearch extends TimeRangeSearch, CallFilterSearch {
  group_by?: CostGroup
  grain?: (typeof GRAINS)[number]
}
export function validateCostSearch(search: unknown): CostSearch {
  const raw = asRaw(search)
  return compact({
    ...validateTimeRange(raw),
    ...validateCallFilters(raw),
    // The default grouping stays out of the URL.
    group_by: oneOf(raw.group_by, COST_GROUPS) === DEFAULT_COST_GROUP ? undefined : oneOf(raw.group_by, COST_GROUPS),
    grain: oneOf(raw.grain, GRAINS),
  })
}

/** The overview dashboard (ADR 0052): a preset range and the activity chart metric. */
export const DASHBOARD_RANGES = ['1h', '24h', '7d', '30d'] as const
export type DashboardRange = (typeof DASHBOARD_RANGES)[number]
export const ACTIVITY_METRICS = ['calls', 'cost'] as const
export type ActivityMetric = (typeof ACTIVITY_METRICS)[number]
/** The period of the cost section (ADR 0059): whole calendar months, the current one included. */
export const COST_RANGES = ['3m', '6m', '12m'] as const
export type CostRange = (typeof COST_RANGES)[number]
export const DEFAULT_COST_RANGE: CostRange = '3m'
export interface DashboardSearch {
  range?: DashboardRange
  metric?: ActivityMetric
  cost_range?: CostRange
}
export function validateDashboardSearch(search: unknown): DashboardSearch {
  const raw = asRaw(search)
  const range = oneOf(raw.range, DASHBOARD_RANGES)
  const metric = oneOf(raw.metric, ACTIVITY_METRICS)
  const costRange = oneOf(raw.cost_range, COST_RANGES)
  // Defaults stay out of the URL: 24h, calls and 3m.
  return compact({
    range: range === DEFAULT_RANGE ? undefined : range,
    metric: metric === 'calls' ? undefined : metric,
    cost_range: costRange === DEFAULT_COST_RANGE ? undefined : costRange,
  })
}

/** The config page has no search parameters; anything in the URL is dropped. */
export function validateNoSearch(): Record<string, never> {
  return {}
}

export { DEFAULT_RANGE }
