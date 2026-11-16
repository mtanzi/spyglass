import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useRem } from '../lib/rem'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import { infiniteListOptions, readQueryOptions } from '../api/queries'
import { ContentNotStored } from '../components/ContentNotStored'
import { DataTable } from '../components/DataTable'
import type { DataTableColumn } from '../components/DataTable'
import {
  BooleanFilter,
  FilterMenu,
  NumberFilter,
  TextFilter,
  TextListFilter,
} from '../components/FilterMenu'
import { Tag } from '../components/Badge'
import { InfoNote } from '../components/InfoNote'
import { ErrorMessage } from '../components/states'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { UNKNOWN, formatBytes, formatCount, formatDurationMs, formatText } from '../lib/format'
import { UNMATCHED } from '../search/routes'
import type { ToolsSearch } from '../search/routes'
import { DEFAULT_RANGE, resolveTimeRange } from '../search/timeRange'
import { formatRatio } from './SessionList'
import {
  TOOL_FILTER_KEYS,
  activeToolFilterCount,
  knownDurations,
  pairingLabel,
  sortingSearch,
  summarySorting,
  summaryTotals,
  toolCallRows,
  toolCallTarget,
  toolCallTime,
  toolFilterQuery,
} from './toolsData'
import type { ToolCall, ToolStat } from './toolsData'

const PAGE_SIZE = 100
/** Row height in rem (32px at the default text size). */
const ROW_SIZE = 2
const SUMMARY_MAX_ROWS = 12
const numeric = { numeric: true }

function Value({ text }: { text: string }) {
  return text === UNKNOWN ? <span className="unknown">{UNKNOWN}</span> : <>{text}</>
}

/** Unknown durations sort last in either direction. */
const durationSort = { sortUndefined: 'last' as const }

const summaryColumns: DataTableColumn<ToolStat>[] = [
  {
    id: 'name',
    header: 'Tool',
    size: 200,
    accessorFn: (s) => s.name,
    sortFn: 'text',
    cell: ({ row }) => <Tag text={formatText(row.original.name)} className="mono" />,
  },
  {
    id: 'agent_type',
    header: 'Agent type',
    size: 140,
    accessorFn: (s) => s.agent_type,
    sortFn: 'text',
    cell: ({ row }) => <Value text={formatText(row.original.agent_type)} />,
  },
  {
    id: 'count',
    header: 'Count',
    size: 80,
    meta: numeric,
    accessorFn: (s) => s.count,
    cell: ({ row }) => <>{formatCount(row.original.count)}</>,
  },
  {
    id: 'error_rate',
    header: 'Error rate',
    size: 110,
    meta: numeric,
    accessorFn: (s) => s.error_rate,
    cell: ({ row }) => {
      const s = row.original
      const text = formatRatio(s.count > 0 ? s.error_rate : null)
      return s.errors > 0 ? (
        <>
          <span className="badge badge-error">{text}</span>
          <small className="dim"> {formatCount(s.errors)} failed</small>
        </>
      ) : (
        <span className="dim">
          <Value text={text} />
        </span>
      )
    },
  },
  {
    id: 'p50',
    header: 'p50',
    size: 90,
    meta: numeric,
    ...durationSort,
    accessorFn: (s) => knownDurations(s).p50 ?? undefined,
    cell: ({ row }) => <Value text={formatDurationMs(knownDurations(row.original).p50)} />,
  },
  {
    id: 'p95',
    header: 'p95',
    size: 90,
    meta: numeric,
    ...durationSort,
    accessorFn: (s) => knownDurations(s).p95 ?? undefined,
    cell: ({ row }) => <Value text={formatDurationMs(knownDurations(row.original).p95)} />,
  },
  {
    id: 'total',
    header: 'Total time',
    size: 110,
    meta: numeric,
    ...durationSort,
    accessorFn: (s) => knownDurations(s).total ?? undefined,
    cell: ({ row }) => <Value text={formatDurationMs(knownDurations(row.original).total)} />,
  },
]

/** Success or error from the result, and the pairing state when the call is not paired. */
function Outcome({ call }: { call: ToolCall }) {
  const pairing = pairingLabel(call.status)
  return (
    <span className="outcome">
      {call.is_error === true && <span className="badge badge-error">Error</span>}
      {call.is_error === false && <span className="badge badge-ok">Success</span>}
      {pairing && (
        <span className="badge badge-pairing" data-status={call.status}>
          {pairing}
        </span>
      )}
      {call.is_error === null && pairing === null && <span className="unknown">{UNKNOWN}</span>}
    </span>
  )
}

const SOURCE_TEXT = {
  header: { label: 'header', title: 'Reported by the client in the prev_tool_durations header' },
  estimated: { label: 'estimated', title: 'Estimated from the gap between the use and the result' },
} as const

/** The duration and where it came from, as a word, not a colour. */
function Duration({ call }: { call: ToolCall }) {
  const source = call.duration_source ? SOURCE_TEXT[call.duration_source] : null
  return (
    <span className="duration-cell">
      <Value text={formatDurationMs(call.duration_ms)} />
      {source && call.duration_ms !== null && (
        <span className="duration-source" data-source={call.duration_source}>
          <InfoNote label={source.label} note={source.title} />
        </span>
      )}
    </span>
  )
}

type Row = { id: string; call: ToolCall }

const stop = (e: React.MouseEvent) => e.stopPropagation()

const listColumns: DataTableColumn<Row>[] = [
  {
    id: 'time',
    header: 'Time',
    size: 165,
    enableSorting: false,
    cell: ({ row }) => {
      const ts = toolCallTime(row.original.call)
      return ts ? (
        <time className="dim num" dateTime={ts}>
          {new Date(ts).toLocaleString()}
        </time>
      ) : (
        <span className="unknown">{UNKNOWN}</span>
      )
    },
  },
  {
    id: 'session',
    header: 'Session and agent',
    size: 200,
    enableSorting: false,
    cell: ({ row }) => {
      const { session_key: session, agent_id: agent } = row.original.call
      return (
        <span className="session-cell">
          {session === '' ? (
            <span className="unknown">{UNKNOWN}</span>
          ) : (
            <Link to="/sessions/$key" params={{ key: session }} search={{}} tabIndex={-1} onClick={stop} className="mono">
              {session}
            </Link>
          )}
          <span className="prompt dim mono">
            <Value text={formatText(agent)} />
          </span>
        </span>
      )
    },
  },
  {
    id: 'tool',
    header: 'Tool',
    size: 160,
    enableSorting: false,
    cell: ({ row }) => <Tag text={formatText(row.original.call.name)} className="mono" />,
  },
  {
    id: 'source',
    header: 'Source',
    size: 70,
    enableSorting: false,
    cell: ({ row }) => <span className="dim">{row.original.call.source}</span>,
  },
  {
    id: 'arg_bytes',
    header: 'Argument size',
    size: 105,
    meta: numeric,
    enableSorting: false,
    // A result with no use has no argument size.
    cell: ({ row }) => (
      <Value text={formatBytes(row.original.call.use_call_id === null ? null : row.original.call.arg_bytes)} />
    ),
  },
  {
    id: 'result_bytes',
    header: 'Result size',
    size: 95,
    meta: numeric,
    enableSorting: false,
    // Zero without a result: unknown, not 0 B.
    cell: ({ row }) => (
      <Value text={formatBytes(row.original.call.result_call_id === null ? null : row.original.call.result_bytes)} />
    ),
  },
  {
    id: 'outcome',
    header: 'Outcome',
    size: 150,
    enableSorting: false,
    cell: ({ row }) => <Outcome call={row.original.call} />,
  },
  {
    id: 'duration',
    header: 'Duration',
    size: 150,
    meta: numeric,
    enableSorting: false,
    cell: ({ row }) => <Duration call={row.original.call} />,
  },
  {
    id: 'calls',
    header: 'Calls',
    size: 130,
    enableSorting: false,
    cell: ({ row }) => {
      const { use_call_id: use, result_call_id: result } = row.original.call
      return (
        <span className="call-links">
          {use ? (
            <Link to="/calls/$id" params={{ id: use }} search={{}} tabIndex={-1} onClick={stop}>
              Request
            </Link>
          ) : (
            <span className="unknown">no request</span>
          )}
          {result ? (
            <Link to="/calls/$id" params={{ id: result }} search={{ tab: 'tools' }} tabIndex={-1} onClick={stop}>
              Result
            </Link>
          ) : (
            <span className="unknown">no result</span>
          )}
        </span>
      )
    },
  },
]

type Patch = Partial<ToolsSearch>

/**
 * V4 tool-call explorer (ADR 0034): a per-tool summary split by agent type
 * and the list of tool calls, newest first. All state is in the URL search
 * parameters. Names, sizes, durations and outcomes only: argument and result
 * text is not stored (ADR 0043).
 */
export function ToolExplorerView() {
  const rem = useRem()
  const search = useSearch({ from: '/tools' }) as ToolsSearch
  const navigate = useNavigate({ from: '/tools' })

  const [now, setNow] = useState(() => new Date())
  const rangeKey = `${search.range ?? ''}|${search.from ?? ''}|${search.to ?? ''}`
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const window = useMemo(() => resolveTimeRange(search, now), [rangeKey, now])
  const filterKey = JSON.stringify(toolFilterQuery(search))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const filters = useMemo(() => toolFilterQuery(search), [filterKey])

  const summary = useQuery(readQueryOptions('/admin/tool-calls/summary', { query: { ...filters, ...window } }))
  const list = useInfiniteQuery(infiniteListOptions('/admin/tool-calls', { ...filters, ...window, limit: PAGE_SIZE }))

  const stats = useMemo(() => summary.data?.tools ?? [], [summary.data])
  const totals = useMemo(() => summaryTotals(stats), [stats])
  const rows = useMemo(() => toolCallRows(list.data?.pages.map((p) => p.tool_calls) ?? []), [list.data])

  const update = (patch: Patch) => {
    if ({ ...search, ...patch }.to === undefined) setNow(new Date())
    void navigate({ search: (prev) => ({ ...prev, ...patch, cursor: undefined }) })
  }
  // The sort only reorders loaded rows: the window stays as it is.
  const setSort = (patch: Pick<ToolsSearch, 'sort' | 'order'>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }) })

  const active = activeToolFilterCount(search)
  const isDefault = search.range === undefined && search.from === undefined && search.to === undefined
  const empty =
    isDefault && active === 0 ? `No tool calls in the last ${DEFAULT_RANGE}.` : 'No tool calls match these filters.'
  const unmatched = search.status !== undefined

  return (
    <section aria-labelledby="view-title">
      <div className="view-header">
        <h1 id="view-title" tabIndex={-1}>Tools</h1>
        <span className="dim">
          {list.data ? `${formatCount(rows.length)}${list.hasNextPage ? '+' : ''} tool calls listed` : ''}
        </span>
      </div>

      <div className="toolbar">
        <search className="toolbar-search">
          <TimeRangePicker value={search} onChange={(next) => update(next)} />
          <FilterMenu
            activeCount={active}
            onClear={() => update(Object.fromEntries(TOOL_FILTER_KEYS.map((k) => [k, undefined])) as Patch)}
          >
            <TextListFilter label="Tool name" values={search.tool} onChange={(v) => update({ tool: v })} />
            <BooleanFilter label="Error" value={search.is_error} onChange={(v) => update({ is_error: v })} />
            <TextFilter label="Session" value={search.session} onChange={(v) => update({ session: v })} />
            <NumberFilter
              label="Duration at least (ms)"
              value={search.duration_ms_gte}
              onChange={(v) => update({ duration_ms_gte: v })}
            />
            <NumberFilter
              label="Argument size at least (bytes)"
              value={search.arg_bytes_gte}
              onChange={(v) => update({ arg_bytes_gte: v })}
            />
            <NumberFilter
              label="Result size at least (bytes)"
              value={search.result_bytes_gte}
              onChange={(v) => update({ result_bytes_gte: v })}
            />
          </FilterMenu>
        </search>
        <label className="toggle">
          <input
            type="checkbox"
            checked={unmatched}
            onChange={(e) => update({ status: e.target.checked ? [...UNMATCHED] : undefined })}
          />
          Unmatched only
        </label>
        <p className="hint dim">
          <kbd>j</kbd> <kbd>k</kbd> move · <kbd>Enter</kbd> open
        </p>
      </div>

      <div className="summary-strip" role="group" aria-label="Summary">
        {summary.isError ? (
          <ErrorMessage error={summary.error} onRetry={() => void summary.refetch()} />
        ) : (
          <dl className="meta-strip">
            <Stat label="Tool calls" value={summary.data ? formatCount(totals.calls) : '…'} />
            <Stat label="Errors" value={summary.data ? formatCount(totals.errors) : '…'} />
            <Stat label="Error rate" value={summary.data ? formatRatio(totals.errorRate) : '…'} />
            <Stat label="Tools" value={summary.data ? formatCount(totals.tools) : '…'} />
          </dl>
        )}
      </div>

      <section aria-labelledby="summary-title" className="tool-section">
        <h2 id="summary-title">Per tool and agent type</h2>
        {!summary.isError && (
          <DataTable<ToolStat>
            label="Tool summary"
            columns={summaryColumns}
            data={stats}
            getRowId={(s) => `${s.name}|${s.agent_type}`}
            isLoading={summary.isPending}
            emptyMessage={empty}
            sorting={summarySorting(search)}
            onSortingChange={(next) => setSort(sortingSearch(next))}
            onRowClick={(s) => {
              if (s.name !== '') update({ tool: [s.name] })
            }}
            rowSize={ROW_SIZE}
            height={Math.min(Math.max(stats.length, 1), SUMMARY_MAX_ROWS) * ROW_SIZE * rem}
            keyboardNavigation
          />
        )}
      </section>

      <section aria-labelledby="list-title" className="tool-section">
        <h2 id="list-title">Tool calls</h2>
        <DataTable<Row>
          label="Tool calls"
          columns={listColumns}
          data={rows}
          getRowId={(r) => r.id}
          isLoading={list.isPending}
          error={list.error}
          onRetry={() => void list.refetch()}
          emptyMessage={empty}
          onRowClick={(r) => {
            const id = toolCallTarget(r.call)
            if (id) void navigate({ to: '/calls/$id', params: { id }, search: { tab: 'tools' } })
          }}
          hasNextPage={list.hasNextPage}
          isFetchingNextPage={list.isFetchingNextPage}
          onEndReached={() => void list.fetchNextPage()}
          rowSize={ROW_SIZE}
          height={560}
          keyboardNavigation
        />
      </section>
      {/* Tool arguments and results are not stored (ADR 0043). */}
      <ContentNotStored />
    </section>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  )
}
