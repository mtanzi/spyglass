import type { QueryOf, ResponseOf } from '../api/client'
import type { CallsSearch } from '../search/routes'

export type CallSummary = ResponseOf<'/admin/calls'>['calls'][number]
export type CallsQuery = Omit<QueryOf<'/admin/calls'>, 'cursor' | 'limit'>

/** At most this many calls arrive through the stream and stay in the list (ADR 0031). */
export const LIVE_ROW_CAP = 500

/** The Agent filter value that stands for the lead agent. */
export const MAIN_AGENT = 'main'

const SHORT_ID_LENGTH = 8

/**
 * The Agent label of a call. Claude Code sends an agent id only from
 * sub-agents, so a main-thread call has a session and an empty agent id.
 * A sub-agent shows its type, or the short agent id when it has no type.
 * A call without a session shows "unknown".
 */
export function agentLabel(session: string, agentId: string, agentType?: string): string {
  if (session === '') return 'unknown'
  if (agentId === '') return MAIN_AGENT
  return agentType || agentId.slice(0, SHORT_ID_LENGTH)
}

/**
 * Whether a call passes the Agent filter. The value "main" matches the label,
 * because main-thread calls store an empty agent id; any other value matches
 * the stored agent id, as the API does.
 */
export function matchesAgentFilter(filter: string | undefined, call: CallSummary): boolean {
  if (filter === undefined) return true
  if (filter === MAIN_AGENT) return agentLabel(call.session_key, call.agent_id) === MAIN_AGENT
  return call.agent_id === filter
}

/** The call-log filter parameters of a search, without time, cursor, grouping or live flag. */
export function filterQuery(search: CallsSearch): CallsQuery {
  return {
    key: search.key,
    client: search.client,
    provider: search.provider,
    model: search.model,
    endpoint: search.endpoint,
    streamed: search.streamed,
    status: search.status,
    error_class: search.error_class,
    // The API takes one value for these three.
    session: search.session?.[0],
    // "main" is matched in the browser: the API compares the stored agent id, which is empty for the lead agent.
    agent: search.agent?.[0] === MAIN_AGENT ? undefined : search.agent?.[0],
    prompt: search.prompt?.[0],
    agent_type: search.agent_type,
    request_class: search.request_class,
    tool: search.tool,
    tool_error: search.tool_error,
    latency_ms_gte: search.latency_ms_gte,
    cost_micro_usd_gte: search.cost_micro_usd_gte,
  }
}

/**
 * Stream query. A preset range is relative to now, so the stream carries no
 * time bounds for it (the stream has no default range); an absolute window is
 * passed as given.
 */
export function streamQuery(search: CallsSearch): CallsQuery {
  return { ...filterQuery(search), from: search.from, to: search.to }
}

/** Number of active filters, for the menu button. The time range is not counted. */
export function activeFilterCount(search: CallsSearch): number {
  const q = { ...filterQuery(search), agent: search.agent?.[0] } as Record<string, unknown>
  return Object.values(q).filter((v) => v !== undefined).length
}

/** Puts a finished call at the top. A call already held is ignored; the oldest rows beyond the cap drop. */
export function insertLive(rows: CallSummary[], call: CallSummary, cap = LIVE_ROW_CAP): CallSummary[] {
  if (rows.some((r) => r.id === call.id)) return rows
  return [call, ...rows].slice(0, cap)
}

/**
 * Live and paged rows, one row per call id, newest first. A reconnect refetch
 * can bring back a call newer than rows already held from the stream. The sort
 * is stable, so calls with the same time keep their order (live, then paged).
 */
export function mergeRows(live: CallSummary[], paged: CallSummary[]): CallSummary[] {
  const seen = new Set<string>()
  const out: CallSummary[] = []
  for (const row of [...live, ...paged]) {
    if (!seen.has(row.id)) {
      seen.add(row.id)
      out.push(row)
    }
  }
  return out.sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts))
}

export type LogRow =
  | { kind: 'call'; id: string; call: CallSummary }
  | { kind: 'group'; id: string; session: string; calls: CallSummary[] }

/** Folds consecutive calls of one session into one row. Calls without a session stay single. */
export function groupBySession(calls: CallSummary[]): LogRow[] {
  const rows: LogRow[] = []
  for (const call of calls) {
    const last = rows[rows.length - 1]
    if (call.session_key !== '' && last?.kind === 'group' && last.session === call.session_key) {
      last.calls.push(call)
    } else if (call.session_key !== '') {
      rows.push({ kind: 'group', id: `group:${call.id}`, session: call.session_key, calls: [call] })
    } else {
      rows.push({ kind: 'call', id: call.id, call })
    }
  }
  // A session with one call is a plain call row.
  return rows.map((r) => (r.kind === 'group' && r.calls.length === 1 ? { kind: 'call', id: r.calls[0].id, call: r.calls[0] } : r))
}

export function plainRows(calls: CallSummary[]): LogRow[] {
  return calls.map((call) => ({ kind: 'call', id: call.id, call }))
}

/** Tokens in: every input column. Zero means not known. */
export function tokensIn(call: CallSummary): number {
  const u = call.usage
  return u.input_uncached + u.cache_read + u.cache_write_5m + u.cache_write_1h
}
