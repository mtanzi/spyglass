import { useInfiniteQuery } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import type { ResponseOf } from '../api/client'
import { infiniteListOptions } from '../api/queries'
import { ContentNotStored } from '../components/ContentNotStored'
import { DataTable } from '../components/DataTable'
import type { DataTableColumn } from '../components/DataTable'
import { FilterMenu, NumberFilter, TextListFilter } from '../components/FilterMenu'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { UNKNOWN, formatCostMicroUsd, formatCount, formatDurationMs, formatText, formatTimeSplit } from '../lib/format'
import type { SessionsSearch } from '../search/routes'
import { DEFAULT_RANGE, resolveTimeRange } from '../search/timeRange'

export type SessionSummary = ResponseOf<'/admin/sessions'>['sessions'][number]

const PAGE_SIZE = 100

/** cache_read over all input columns; null without input tokens (as the usage endpoint). */
export function cacheHitRatio(usage: SessionSummary['usage']): number | null {
  const input = usage.input_uncached + usage.cache_read + usage.cache_write_5m + usage.cache_write_1h
  return input > 0 ? usage.cache_read / input : null
}

export function formatRatio(value: number | null): string {
  return value == null ? UNKNOWN : `${(value * 100).toFixed(1)}%`
}

/** Duration of a session in the range: first call start to the latest call end. */
export function sessionDurationMs(s: Pick<SessionSummary, 'first_ts' | 'ended_at'>): number | null {
  const span = Date.parse(s.ended_at) - Date.parse(s.first_ts)
  return Number.isFinite(span) && span >= 0 ? span : null
}

function Cell({ text }: { text: string }) {
  return text === UNKNOWN ? <span className="unknown">{UNKNOWN}</span> : <>{text}</>
}

const columns: DataTableColumn<SessionSummary>[] = [
  {
    id: 'start',
    header: 'Start',
    size: 170,
    accessorFn: (s) => Date.parse(s.first_ts),
    cell: ({ row }) => <time dateTime={row.original.first_ts}>{new Date(row.original.first_ts).toLocaleString()}</time>,
  },
  {
    id: 'session',
    header: 'Session',
    size: 220,
    enableSorting: false,
    cell: ({ row }) => (
      <Link
        to="/sessions/$key"
        params={{ key: row.original.session_key }}
        search={{}}
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
        className="mono"
      >
        {row.original.session_key}
      </Link>
    ),
  },
  {
    id: 'duration',
    meta: { numeric: true },
    header: 'Duration',
    size: 100,
    accessorFn: (s) => sessionDurationMs(s),
    cell: ({ row }) => <Cell text={formatDurationMs(sessionDurationMs(row.original))} />,
  },
  {
    id: 'time_split',
    header: 'Model / tool time',
    size: 330,
    enableSorting: false,
    cell: ({ row }) => (
      <Cell text={formatTimeSplit(row.original.llm_ms, row.original.tool_ms, row.original.tool_ms_estimated)} />
    ),
  },
  {
    id: 'client',
    header: 'Client',
    size: 120,
    enableSorting: false,
    cell: ({ row }) => <Cell text={formatText(row.original.client)} />,
  },
  {
    id: 'key',
    header: 'Key',
    size: 120,
    enableSorting: false,
    cell: ({ row }) => <Cell text={formatText(row.original.key)} />,
  },
  {
    id: 'prompts',
    meta: { numeric: true },
    header: 'Prompts',
    size: 80,
    accessorFn: (s) => s.prompts,
    cell: ({ row }) => <>{formatCount(row.original.prompts)}</>,
  },
  {
    id: 'calls',
    meta: { numeric: true },
    header: 'Calls',
    size: 70,
    accessorFn: (s) => s.calls,
    cell: ({ row }) => <>{formatCount(row.original.calls)}</>,
  },
  {
    id: 'tool_calls',
    meta: { numeric: true },
    header: 'Tool calls',
    size: 90,
    accessorFn: (s) => s.tool_calls,
    cell: ({ row }) => <>{formatCount(row.original.tool_calls)}</>,
  },
  {
    id: 'cost',
    meta: { numeric: true },
    header: 'Cost',
    size: 100,
    accessorFn: (s) => s.cost_micro_usd,
    cell: ({ row }) => <Cell text={formatCostMicroUsd(row.original.cost_micro_usd)} />,
  },
  {
    id: 'cache_hit',
    meta: { numeric: true },
    header: 'Cache hit',
    size: 90,
    accessorFn: (s) => cacheHitRatio(s.usage),
    cell: ({ row }) => <Cell text={formatRatio(cacheHitRatio(row.original.usage))} />,
  },
  {
    id: 'errors',
    meta: { numeric: true },
    header: 'Errors',
    size: 70,
    accessorFn: (s) => s.errors,
    cell: ({ row }) =>
      row.original.errors > 0 ? (
        <span className="badge badge-error">{formatCount(row.original.errors)}</span>
      ) : (
        <span className="dim">0</span>
      ),
  },
]

type Patch = Partial<SessionsSearch>

const FILTER_KEYS = ['key', 'client', 'provider', 'model', 'error_class', 'cost_micro_usd_gte'] as const

/** V2 session list (ADR 0033): one row per session, newest first. */
export function SessionListView() {
  const search = useSearch({ from: '/sessions/' }) as SessionsSearch
  const navigate = useNavigate({ from: '/sessions/' })
  const [now, setNow] = useState(() => new Date())
  const rangeKey = `${search.range ?? ''}|${search.from ?? ''}|${search.to ?? ''}`
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const window = useMemo(() => resolveTimeRange(search, now), [rangeKey, now])

  const query = {
    ...window,
    key: search.key,
    client: search.client,
    provider: search.provider,
    model: search.model,
    error_class: search.error_class,
    cost_micro_usd_gte: search.cost_micro_usd_gte,
    limit: PAGE_SIZE,
  }
  const list = useInfiniteQuery(infiniteListOptions('/admin/sessions', query))
  const rows = useMemo(() => list.data?.pages.flatMap((p) => p.sessions) ?? [], [list.data])

  const update = (patch: Patch) => {
    if ({ ...search, ...patch }.to === undefined) setNow(new Date())
    void navigate({ search: (prev) => ({ ...prev, ...patch, cursor: undefined }) })
  }
  const active = FILTER_KEYS.filter((k) => search[k] !== undefined).length
  const isDefault = search.range === undefined && search.from === undefined && search.to === undefined

  return (
    <section aria-labelledby="view-title">
      <div className="view-header">
        <h1 id="view-title" tabIndex={-1}>Sessions</h1>
        <span className="dim">{list.data ? `${formatCount(rows.length)}${list.hasNextPage ? '+' : ''} sessions` : ''}</span>
      </div>
      <div className="toolbar">
        <search className="toolbar-search">
          <TimeRangePicker value={search} onChange={(next) => update(next)} />
          <FilterMenu
            activeCount={active}
            onClear={() => update(Object.fromEntries(FILTER_KEYS.map((k) => [k, undefined])) as Patch)}
          >
            <TextListFilter label="Key name" values={search.key} onChange={(v) => update({ key: v })} />
            <TextListFilter label="Client" values={search.client} onChange={(v) => update({ client: v })} />
            <TextListFilter label="Provider" values={search.provider} onChange={(v) => update({ provider: v })} />
            <TextListFilter label="Model" values={search.model} onChange={(v) => update({ model: v })} />
            <TextListFilter label="Error class" values={search.error_class} onChange={(v) => update({ error_class: v })} />
            <NumberFilter
              label="Cost at least (micro-USD)"
              value={search.cost_micro_usd_gte}
              onChange={(v) => update({ cost_micro_usd_gte: v })}
            />
          </FilterMenu>
        </search>
        <p className="hint dim">
          <kbd>j</kbd> <kbd>k</kbd> move · <kbd>Enter</kbd> open
        </p>
      </div>
      <p className="hint dim">
        Model / tool time: shares of summed model time and tool time. Parallel tool calls and sub-agents overlap in
        wall time, so these are not shares of Duration. “est.” marks a tool time with an estimated duration.
      </p>
      <DataTable<SessionSummary>
        label="Sessions"
        columns={columns}
        data={rows}
        getRowId={(s) => s.session_key}
        isLoading={list.isPending}
        error={list.error}
        onRetry={() => void list.refetch()}
        emptyMessage={
          isDefault && active === 0 ? `No sessions in the last ${DEFAULT_RANGE}.` : 'No sessions match these filters.'
        }
        onRowClick={(s) => void navigate({ to: '/sessions/$key', params: { key: s.session_key }, search: {} })}
        hasNextPage={list.hasNextPage}
        isFetchingNextPage={list.isFetchingNextPage}
        onEndReached={() => void list.fetchNextPage()}
        rowSize={2}
        height={600}
        keyboardNavigation
      />
      {/* No prompt text is stored, so sessions are labelled by key, client and start time. */}
      <ContentNotStored />
    </section>
  )
}
