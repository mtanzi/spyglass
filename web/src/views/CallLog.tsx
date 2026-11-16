import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { infiniteListOptions, readQueryOptions } from '../api/queries'
import { listQueryKey } from '../api/keys'
import { DataTable } from '../components/DataTable'
import type { DataTableColumn } from '../components/DataTable'
import {
  BooleanFilter,
  FilterMenu,
  NumberFilter,
  TextFilter,
  TextListFilter,
} from '../components/FilterMenu'
import { StatusBadge, Tag, isErrorStatus } from '../components/Badge'
import { InfoNote } from '../components/InfoNote'
import { ErrorMessage } from '../components/states'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { VolumeChart } from '../components/VolumeChart'
import type { Grain } from '../components/VolumeChart'
import { useLiveCalls } from '../hooks/useLiveCalls'
import type { LiveStatus } from '../hooks/useLiveCalls'
import {
  formatCappedCount,
  formatCostMicroUsd,
  formatCount,
  formatDurationMs,
  formatText,
  zeroAsUnknown,
} from '../lib/format'
import type { CallsSearch } from '../search/routes'
import { DEFAULT_RANGE, resolveTimeRange } from '../search/timeRange'
import {
  activeFilterCount,
  filterQuery,
  groupBySession,
  mergeRows,
  plainRows,
  streamQuery,
  tokensIn,
} from './callLogRows'
import type { CallSummary, LogRow } from './callLogRows'

const PAGE_SIZE = 100

function grainFor(from: string, to: string): Grain {
  const span = Date.parse(to) - Date.parse(from)
  if (span <= 2 * 3_600_000) return 'minute'
  if (span <= 3 * 86_400_000) return 'hour'
  return 'day'
}

type Measure = (c: CallSummary) => number | null

function sumKnown(calls: CallSummary[], measure: Measure): number | null {
  const known = calls.map(measure).filter((v): v is number => v !== null)
  return known.length === 0 ? null : known.reduce((a, b) => a + b, 0)
}

/** The first failed call of a group, or its first call. */
function worstCall(calls: CallSummary[]): CallSummary {
  return calls.find((c) => isErrorStatus(c.status, c.error_class)) ?? calls[0]
}

function Unknown() {
  return <span className="unknown">unknown</span>
}

function Maybe({ text }: { text: string }) {
  return text === 'unknown' ? <Unknown /> : <>{text}</>
}

function turnsLabel(row: LogRow): string | null {
  return row.kind === 'group' ? `${row.calls.length} turns` : null
}

const rowCalls = (row: LogRow): CallSummary[] => (row.kind === 'group' ? row.calls : [row.call])
const first = (row: LogRow): CallSummary => rowCalls(row)[0]

function promptLabel(row: LogRow): string {
  const prompts = new Set(rowCalls(row).map((c) => c.prompt_id ?? '').filter((id) => id !== ''))
  return prompts.size > 1 ? `${prompts.size} prompts` : formatText([...prompts][0])
}

/** A group total over the known values; the turns whose value is not known are counted next to it. */
function Total({ row, measure, format }: { row: LogRow; measure: Measure; format: (v: number | null) => string }) {
  const calls = rowCalls(row)
  const total = sumKnown(calls, measure)
  const missing = calls.filter((c) => measure(c) === null).length
  const shown = total !== null && missing > 0
  // The cell shows a compact "+N?" marker so the total fits; its note spells it out.
  // The title only repeats the shown total, which a narrow column may clip.
  return (
    <span title={format(total)}>
      <Maybe text={format(total)} />
      {shown && (
        <>
          {' '}
          <InfoNote
            className="unknown"
            label={`+${missing}?`}
            note={`${format(total)} is the total of the known values. ${missing} ${missing === 1 ? 'call has' : 'calls have'} no value and ${missing === 1 ? 'is' : 'are'} left out.`}
          />
        </>
      )}
    </span>
  )
}

const latency: Measure = (c) => zeroAsUnknown(c.duration_ms)
const inputTokens: Measure = (c) => zeroAsUnknown(tokensIn(c))
const outputTokens: Measure = (c) => zeroAsUnknown(c.usage.output)
const cost: Measure = (c) => c.cost_micro_usd
const numeric = { numeric: true }

const columns: DataTableColumn<LogRow>[] = [
  {
    id: 'time',
    header: 'Time',
    size: 165,
    enableSorting: false,
    cell: ({ row }) => (
      <time className="dim num" dateTime={first(row.original).ts}>
        {new Date(first(row.original).ts).toLocaleString()}
      </time>
    ),
  },
  {
    id: 'session',
    header: 'Session and prompt',
    size: 150,
    enableSorting: false,
    cell: ({ row }) => {
      const session = first(row.original).session_key
      const turns = turnsLabel(row.original)
      return (
        <span className="session-cell">
          {session === '' ? (
            <Unknown />
          ) : (
            <Link
              to="/sessions/$key"
              params={{ key: session }}
              tabIndex={-1}
              onClick={(e) => e.stopPropagation()}
              className="mono"
            >
              {session}
            </Link>
          )}
          {turns && <span className="badge">{turns}</span>}
          <span className="prompt dim">
            <Maybe text={promptLabel(row.original)} />
          </span>
        </span>
      )
    },
  },
  {
    id: 'agent',
    header: 'Agent',
    size: 80,
    enableSorting: false,
    cell: ({ row }) => (
      <span className="mono dim">
        <Maybe text={formatText(first(row.original).agent_id)} />
      </span>
    ),
  },
  {
    id: 'client',
    header: 'Client',
    size: 100,
    enableSorting: false,
    cell: ({ row }) => <Tag text={formatText(first(row.original).client)} />,
  },
  {
    id: 'model',
    header: 'Model',
    size: 150,
    enableSorting: false,
    cell: ({ row }) => <Tag text={formatText(first(row.original).model)} className="mono" />,
  },
  {
    id: 'status',
    header: 'Status',
    // Fits the widest status and error class badge, such as "502 · upstream_error".
    size: 160,
    enableSorting: false,
    cell: ({ row }) => {
      const c = worstCall(rowCalls(row.original))
      return <StatusBadge status={c.status} errorClass={c.error_class} />
    },
  },
  {
    id: 'latency',
    header: 'Latency',
    size: 76,
    meta: numeric,
    accessorFn: (r) => sumKnown(rowCalls(r), latency),
    cell: ({ row }) => <Total row={row.original} measure={latency} format={formatDurationMs} />,
  },
  {
    id: 'tokens_in',
    header: 'Tokens in',
    // Fits a grouped total with its marker, such as "220,210 +1?".
    size: 92,
    meta: numeric,
    accessorFn: (r) => sumKnown(rowCalls(r), inputTokens),
    cell: ({ row }) => <Total row={row.original} measure={inputTokens} format={formatCount} />,
  },
  {
    id: 'tokens_out',
    header: 'Tokens out',
    size: 92,
    meta: numeric,
    accessorFn: (r) => sumKnown(rowCalls(r), outputTokens),
    cell: ({ row }) => <Total row={row.original} measure={outputTokens} format={formatCount} />,
  },
  {
    id: 'cost',
    header: 'Cost',
    // Fits a grouped sub-cent cost with its marker, such as "$0.007512 +1?".
    size: 110,
    meta: numeric,
    accessorFn: (r) => sumKnown(rowCalls(r), cost),
    cell: ({ row }) => <Total row={row.original} measure={cost} format={formatCostMicroUsd} />,
  },
  {
    id: 'tools',
    header: 'Tool calls',
    size: 70,
    meta: numeric,
    accessorFn: (r) => rowCalls(r).reduce((sum, c) => sum + c.tool_count, 0),
    cell: ({ getValue }) => <>{formatCount(getValue<number>())}</>,
  },
]

const STREAM_LABELS: Record<Exclude<LiveStatus, 'off'>, string> = {
  live: 'Live',
  reconnecting: 'Reconnecting…',
  closed: 'Stream closed',
  connecting: 'Connecting…',
}

type FilterPatch = Partial<CallsSearch>

/**
 * V1 call log (ADR 0031). All state is in the URL search
 * parameters. Linear look as the session pages (ADR 0033): dense rows,
 * right-aligned numbers, status badges, and j/k or arrow keys to move between
 * rows with Enter to open one.
 */
export function CallLogView() {
  const search = useSearch({ from: '/calls/' }) as CallsSearch
  const navigate = useNavigate({ from: '/calls/' })
  const queryClient = useQueryClient()

  // The window is fixed per set of search parameters and `now`, so the query key is stable.
  // `now` moves when the list must catch up with calls the stream may have missed.
  const [now, setNow] = useState(() => new Date())
  const rangeKey = `${search.range ?? ''}|${search.from ?? ''}|${search.to ?? ''}`
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const window = useMemo(() => resolveTimeRange(search, now), [rangeKey, now])
  const filterKey = JSON.stringify(filterQuery(search))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const filters = useMemo(() => filterQuery(search), [filterKey])
  const live = search.live === true

  const listQuery = { ...filters, ...window, limit: PAGE_SIZE }
  const grain = grainFor(window.from, window.to)
  const list = useInfiniteQuery(infiniteListOptions('/admin/calls', listQuery, { live }))
  const usage = useQuery(
    readQueryOptions(
      '/admin/usage',
      { query: { ...filters, ...window, grain } },
      { live },
    ),
  )

  const streamKey = JSON.stringify(streamQuery(search))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const streamParams = useMemo(() => streamQuery(search), [streamKey])
  // The stream does not replay: reload the first page, up to now, after a reconnect or when live starts.
  const catchUp = () => {
    if (search.to === undefined) setNow(new Date())
    else void queryClient.resetQueries({ queryKey: listQueryKey('/admin/calls', listQuery) })
  }
  const stream = useLiveCalls(live, streamParams, catchUp)

  const paged = useMemo(() => list.data?.pages.flatMap((p) => p.calls) ?? [], [list.data])
  const calls = useMemo(() => mergeRows(stream.rows, paged), [stream.rows, paged])
  const rows = useMemo(
    () => (search.group === 'session' ? groupBySession(calls) : plainRows(calls)),
    [calls, search.group],
  )

  const update = (patch: FilterPatch) => {
    if ({ ...search, ...patch }.to === undefined) setNow(new Date())
    void navigate({ search: (prev) => ({ ...prev, ...patch, cursor: undefined }) })
  }

  const firstPage = list.data?.pages[0]
  const buckets = usage.data?.buckets ?? []
  const totals = useMemo(() => summarize(usage.data?.buckets ?? []), [usage.data])
  const isDefaultRange =
    search.range === undefined && search.from === undefined && search.to === undefined

  const filterCount = activeFilterCount(search)

  return (
    <section aria-labelledby="view-title">
      <div className="view-header">
        <h1 id="view-title" tabIndex={-1}>Calls</h1>
      </div>

      <div className="toolbar">
        <search className="toolbar-search">
          <TimeRangePicker value={search} onChange={(next) => update(next)} />
          <FilterMenu
            activeCount={filterCount}
            onClear={() =>
              update(Object.fromEntries(Object.keys(filterQuery(search)).map((k) => [k, undefined])) as FilterPatch)
            }
          >
            <TextListFilter label="Key name" values={search.key} onChange={(v) => update({ key: v })} />
            <TextListFilter label="Client" values={search.client} onChange={(v) => update({ client: v })} />
            <TextListFilter label="Provider" values={search.provider} onChange={(v) => update({ provider: v })} />
            <TextListFilter label="Model" values={search.model} onChange={(v) => update({ model: v })} />
            <TextListFilter label="Endpoint" values={search.endpoint} onChange={(v) => update({ endpoint: v })} />
            <TextListFilter
              label="Status"
              values={search.status?.map(String)}
              onChange={(v) => update({ status: v?.map(Number).filter((n) => Number.isInteger(n) && n >= 100 && n <= 599) })}
            />
            <TextListFilter label="Error class" values={search.error_class} onChange={(v) => update({ error_class: v })} />
            <TextFilter label="Session" value={search.session?.[0]} onChange={(v) => update({ session: v ? [v] : undefined })} />
            <TextFilter label="Agent" value={search.agent?.[0]} onChange={(v) => update({ agent: v ? [v] : undefined })} />
            <TextListFilter label="Agent type" values={search.agent_type} onChange={(v) => update({ agent_type: v })} />
            <TextFilter label="Prompt" value={search.prompt?.[0]} onChange={(v) => update({ prompt: v ? [v] : undefined })} />
            <TextListFilter label="Request class" values={search.request_class} onChange={(v) => update({ request_class: v })} />
            <TextListFilter label="Tool name" values={search.tool} onChange={(v) => update({ tool: v })} />
            <BooleanFilter label="Streamed" value={search.streamed} onChange={(v) => update({ streamed: v })} />
            <BooleanFilter label="Tool error" value={search.tool_error} onChange={(v) => update({ tool_error: v })} />
            <NumberFilter label="Latency at least (ms)" value={search.latency_ms_gte} onChange={(v) => update({ latency_ms_gte: v })} />
            <NumberFilter label="Cost at least (micro-USD)" value={search.cost_micro_usd_gte} onChange={(v) => update({ cost_micro_usd_gte: v })} />
          </FilterMenu>
        </search>
        <label className="toggle">
          <input
            type="checkbox"
            checked={search.group === 'session'}
            onChange={(e) => update({ group: e.target.checked ? 'session' : undefined })}
          />
          Group by session
        </label>
        <label className="toggle">
          <input
            type="checkbox"
            checked={live}
            onChange={(e) => {
              if (e.target.checked) catchUp()
              update({ live: e.target.checked ? true : undefined })
            }}
          />
          Live
        </label>
        {live && stream.status !== 'off' && (
          <span className="live-status" data-status={stream.status} role="status" aria-live="polite">
            {STREAM_LABELS[stream.status]}
          </span>
        )}
        {live && stream.status === 'closed' && (
          <button type="button" onClick={stream.retry}>
            Retry stream
          </button>
        )}
        <p className="hint dim">
          <kbd>j</kbd> <kbd>k</kbd> move · <kbd>Enter</kbd> open
        </p>
      </div>

      <div className="summary-strip" role="group" aria-label="Summary">
        {usage.isError ? (
          <ErrorMessage error={usage.error} onRetry={() => void usage.refetch()} />
        ) : (
          <dl className="meta-strip">
            <Stat label="Calls" value={firstPage ? formatCappedCount(firstPage.count, firstPage.count_capped) : usage.data ? formatCount(totals.calls) : '…'} />
            <Stat label="Errors" value={usage.data ? formatCount(totals.errors) : '…'} />
            <Stat label="Tokens in" value={usage.data ? formatCount(totals.tokensIn) : '…'} />
            <Stat label="Tokens out" value={usage.data ? formatCount(totals.tokensOut) : '…'} />
            <Stat
              label="Cost"
              value={usage.data ? formatCostMicroUsd(totals.cost) : '…'}
              note={totals.unpriced > 0 ? `${formatCount(totals.unpriced)} calls unpriced` : undefined}
            />
          </dl>
        )}
      </div>

      {!usage.isError && <VolumeChart points={buckets} from={window.from} to={window.to} grain={grain} />}

      <DataTable<LogRow>
        label="Calls"
        columns={columns}
        data={rows}
        getRowId={(r) => r.id}
        isLoading={list.isPending}
        error={list.error}
        onRetry={() => void list.refetch()}
        emptyMessage={
          isDefaultRange && filterCount === 0
            ? `No calls in the last ${DEFAULT_RANGE}.`
            : 'No calls match these filters.'
        }
        onRowClick={(r) =>
          r.kind === 'group'
            ? void navigate({ to: '/sessions/$key', params: { key: r.session }, search: {} })
            : void navigate({ to: '/calls/$id', params: { id: r.call.id }, search: {} })
        }
        hasNextPage={list.hasNextPage}
        isFetchingNextPage={list.isFetchingNextPage}
        onEndReached={() => void list.fetchNextPage()}
        rowSize={2}
        height={560}
        keyboardNavigation
      />
    </section>
  )
}

function Stat({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        {value}
        {note && <small className="unknown"> ({note})</small>}
      </dd>
    </div>
  )
}

function summarize(buckets: { calls: number; errors: number; cost_micro_usd: number; unpriced_calls: number; usage: CallSummary['usage'] }[]) {
  let calls = 0, errors = 0, tin = 0, tout = 0, cost = 0, unpriced = 0
  for (const b of buckets) {
    calls += b.calls
    errors += b.errors
    tin += b.usage.input_uncached + b.usage.cache_read + b.usage.cache_write_5m + b.usage.cache_write_1h
    tout += b.usage.output
    cost += b.cost_micro_usd
    unpriced += b.unpriced_calls
  }
  // All calls unpriced means the cost is unknown, not zero.
  return { calls, errors, tokensIn: tin, tokensOut: tout, cost: calls > 0 && unpriced === calls ? null : cost, unpriced }
}
