import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import type { QueryOf } from '../api/client'
import { readQueryOptions } from '../api/queries'
import { ActivityChart } from '../components/ActivityChart'
import { StatusBadge, Tag } from '../components/Badge'
import { InfoNote } from '../components/InfoNote'
import { Sparkline } from '../components/Sparkline'
import { EmptyState, ErrorMessage, LoadingState } from '../components/states'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { useLiveCalls } from '../hooks/useLiveCalls'
import type { LiveStatus } from '../hooks/useLiveCalls'
import {
  UNKNOWN,
  formatCostMicroUsd,
  formatCount,
  formatDurationMs,
  formatText,
  PASSTHROUGH_NOTE,
  zeroAsUnknown,
} from '../lib/format'
import { DASHBOARD_RANGES } from '../search/routes'
import type { ActivityMetric, DashboardRange, DashboardSearch } from '../search/routes'
import { resolveTimeRange } from '../search/timeRange'
import { mergeRows, tokensIn } from './callLogRows'
import type { CallSummary } from './callLogRows'
import {
  RANGE_GRAIN,
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
import type { Change, GroupTotal, Measure, Totals, UsageBucket } from './dashboardData'
import { formatRatio, sessionDurationMs } from './SessionList'

const FEED_SIZE = 8
const SESSION_COUNT = 5
const RANK_SIZE = 5
const DEFAULT: DashboardRange = '24h'

const STREAM_LABELS: Record<Exclude<LiveStatus, 'off'>, string> = {
  live: 'Live',
  reconnecting: 'Reconnecting…',
  closed: 'Stream closed',
  connecting: 'Connecting…',
}

function Maybe({ text }: { text: string }) {
  return text === UNKNOWN ? <span className="unknown">{UNKNOWN}</span> : <>{text}</>
}

function Panel({
  id,
  title,
  action,
  className,
  children,
}: {
  id: string
  title: string
  action?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <section className={className ? `panel ${className}` : 'panel'} aria-labelledby={id}>
      <div className="panel-head">
        <h2 id={id}>{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}

/** Loading, error and content of a panel from one query. */
function QueryState({
  query,
  children,
}: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown }
  children: () => ReactNode
}) {
  if (query.isError) return <ErrorMessage error={query.error} onRetry={() => void query.refetch()} />
  if (query.isPending) return <LoadingState />
  return <>{children()}</>
}

type Bad = 'up' | 'down' | 'none'

interface TileSpec {
  label: string
  value: string
  approxNote?: string
  note?: string
  measure: Measure
  current: number | null
  previous: number | null
  mode: 'relative' | 'points'
  bad: Bad
}

function toneOf(change: Change, bad: Bad): 'bad' | 'good' | 'neutral' {
  if (bad === 'none' || (change.kind !== 'up' && change.kind !== 'down')) return 'neutral'
  return change.kind === bad ? 'bad' : 'good'
}

/** The change against the previous period: a glyph, a signed number and words, never colour alone. */
function ChangeText({ change, mode, bad, range }: { change: Change; mode: 'relative' | 'points'; bad: Bad; range: DashboardRange }) {
  const glyph = change.kind === 'up' ? '▲' : change.kind === 'down' ? '▼' : ''
  return (
    <span className="change" data-tone={toneOf(change, bad)}>
      {glyph && <span aria-hidden="true">{glyph} </span>}
      {formatChange(change, mode)}
      <span className="dim"> vs previous {range}</span>
    </span>
  )
}

function Tiles({
  cur,
  prev,
  series,
  range,
  costNote,
}: {
  cur: Totals | undefined
  prev: Totals | undefined
  series: (m: Measure) => (number | null)[]
  range: DashboardRange
  costNote?: string
}) {
  const latencyNote =
    cur && !cur.latencyExact
      ? 'Approximate: the call-weighted mean of the per-interval percentiles. The usage API has no percentile over the whole range.'
      : undefined
  const approx = (v: number | null) => (cur && !cur.latencyExact && v !== null ? '≈ ' : '') + formatDurationMs(v)
  const unpricedNote = cur && cur.unpriced > 0 && cur.cost !== null ? `${formatCount(cur.unpriced)} calls unpriced` : undefined
  const tiles: TileSpec[] = [
    { label: 'Calls', value: formatCount(cur?.calls), measure: 'calls', current: cur?.calls ?? null, previous: prev?.calls ?? null, mode: 'relative', bad: 'none' },
    {
      label: 'Cost',
      value: formatCostMicroUsd(cur?.cost),
      note: [costNote, unpricedNote].filter(Boolean).join(' · ') || undefined,
      measure: 'cost',
      current: cur?.cost ?? null,
      previous: prev?.cost ?? null,
      mode: 'relative',
      bad: 'up',
    },
    { label: 'Tokens in', value: formatCount(cur?.tokensIn), measure: 'tokensIn', current: cur?.tokensIn ?? null, previous: prev?.tokensIn ?? null, mode: 'relative', bad: 'none' },
    { label: 'Tokens out', value: formatCount(cur?.tokensOut), measure: 'tokensOut', current: cur?.tokensOut ?? null, previous: prev?.tokensOut ?? null, mode: 'relative', bad: 'none' },
    { label: 'Error rate', value: formatRatio(cur?.errorRate ?? null), measure: 'errorRate', current: cur?.errorRate ?? null, previous: prev?.errorRate ?? null, mode: 'points', bad: 'up' },
    { label: 'Cache hit', value: formatRatio(cur?.cacheHit ?? null), measure: 'cacheHit', current: cur?.cacheHit ?? null, previous: prev?.cacheHit ?? null, mode: 'points', bad: 'down' },
    { label: 'Latency p50', value: approx(cur?.p50 ?? null), approxNote: latencyNote, measure: 'p50', current: cur?.p50 ?? null, previous: prev?.p50 ?? null, mode: 'relative', bad: 'up' },
    { label: 'Latency p95', value: approx(cur?.p95 ?? null), approxNote: latencyNote, measure: 'p95', current: cur?.p95 ?? null, previous: prev?.p95 ?? null, mode: 'relative', bad: 'up' },
  ]
  return (
    <dl className="kpi-grid">
      {tiles.map((t) => (
        <div key={t.label} className="kpi">
          <dt>{t.label}</dt>
          <dd>
            <span className="kpi-value">{cur ? <Maybe text={t.value} /> : '…'}</span>
            {t.approxNote && <InfoNote label="approximate" note={t.approxNote} />}
            {t.note && <small className="kpi-note">{t.note}</small>}
            {cur && prev && (
              <ChangeText change={compare(t.current, t.previous, t.mode)} mode={t.mode} bad={t.bad} range={range} />
            )}
            {cur && <Sparkline values={series(t.measure)} tone={t.measure === 'errorRate' ? 'error' : 'neutral'} />}
          </dd>
        </div>
      ))}
    </dl>
  )
}

/** A ranked list with a bar per row; the bar is the share of the known cost of the list. */
function RankedList({
  label,
  rows,
  total,
  link,
}: {
  label: string
  rows: GroupTotal[]
  total: number
  link?: (group: string) => ReactNode
}) {
  if (rows.length === 0) return <EmptyState>No calls in this range.</EmptyState>
  const max = Math.max(1, ...rows.map((r) => r.cost ?? 0))
  return (
    <ol className="ranked" aria-label={label}>
      {rows.map((r) => {
        const name = formatText(r.group)
        const share = r.cost !== null && total > 0 ? `${((r.cost / total) * 100).toFixed(0)}%` : ''
        return (
          <li key={r.group}>
            <span className="ranked-name">{link && r.group !== '' ? link(r.group) : <Tag text={name} className="mono" />}</span>
            <span className="ranked-value num">
              <Maybe text={formatCostMicroUsd(r.cost)} />
              {share && <span className="dim"> · {share}</span>}
            </span>
            <span className="ranked-bar" aria-hidden="true">
              <span style={{ width: `${(((r.cost ?? 0) / max) * 100).toFixed(1)}%` }} />
            </span>
          </li>
        )
      })}
    </ol>
  )
}

function knownCost(rows: GroupTotal[]): number {
  return rows.reduce((sum, r) => sum + (r.cost ?? 0), 0)
}

/**
 * Overview dashboard (ADR 0052): the landing page at /ui. Headline numbers
 * against the previous equal period, activity by model, a live feed, recent
 * sessions, problems and cost breakdowns, for one preset range in the URL.
 */
export function DashboardView() {
  const search = useSearch({ from: '/' }) as DashboardSearch
  const navigate = useNavigate({ from: '/' })
  const range: DashboardRange = search.range ?? DEFAULT
  const metric: ActivityMetric = search.metric ?? 'calls'

  // The window is fixed per range and `now`, so the query keys are stable until Refresh.
  const [now, setNow] = useState(() => new Date())
  const window = useMemo(() => resolveTimeRange({ range }, now), [range, now])
  const prevWindow = useMemo(() => previousWindow(window), [window])
  const grain = RANGE_GRAIN[range]
  const slots = useMemo(() => timeSlots(window, grain), [window, grain])

  const usage = (query: Omit<QueryOf<'/admin/usage'>, 'from' | 'to'>) => readQueryOptions('/admin/usage', { query: { ...window, ...query } })
  const current = useQuery(usage({ grain }))
  const previous = useQuery(readQueryOptions('/admin/usage', { query: { ...prevWindow, grain } }))
  const byModel = useQuery(usage({ grain, group_by: 'model' }))
  // Breakdowns sum over the range, so the widest grain keeps them small.
  const byClient = useQuery(usage({ grain: 'day', group_by: 'client' }))
  const byKey = useQuery(usage({ grain: 'day', group_by: 'key' }))
  const bySession = useQuery(usage({ grain: 'day', group_by: 'session' }))
  const byError = useQuery(usage({ grain: 'day', group_by: 'error_class' }))
  const config = useQuery(readQueryOptions('/admin/config'))
  const passthrough = useMemo(() => passthroughProviders(config.data?.providers), [config.data])
  // Only a client_passthrough provider makes part of the cost API-equivalent.
  const byProvider = useQuery({ ...usage({ grain: 'day', group_by: 'provider' }), enabled: passthrough.size > 0 })
  const sessions = useQuery(readQueryOptions('/admin/sessions', { query: { ...window, limit: SESSION_COUNT } }))
  // The feed has no end, so a refetch after a reconnect brings calls up to now.
  const recent = useQuery(readQueryOptions('/admin/calls', { query: { from: window.from, limit: FEED_SIZE } }))
  const stream = useLiveCalls(true, {}, () => void recent.refetch())

  const cur = useMemo(() => (current.data ? totals(current.data.buckets) : undefined), [current.data])
  const prev = useMemo(() => (previous.data ? totals(previous.data.buckets) : undefined), [previous.data])
  const series = (m: Measure) => slotSeries(current.data?.buckets ?? [], slots, m)

  const apiEquivalent = byProvider.data ? apiEquivalentCost(byProvider.data.buckets, passthrough) : 0
  const costNote =
    apiEquivalent <= 0 || cur?.cost == null
      ? undefined
      : apiEquivalent >= cur.cost
        ? 'API-equivalent'
        : `incl. ${formatCostMicroUsd(apiEquivalent)} API-equivalent`

  const feed = useMemo(
    () => mergeRows(stream.rows, recent.data?.calls ?? []).slice(0, FEED_SIZE),
    [stream.rows, recent.data],
  )
  const stack = useMemo(
    () => stackByGroup(byModel.data?.buckets ?? [], slots, metric),
    [byModel.data, slots, metric],
  )

  const setSearch = (patch: Partial<DashboardSearch>) => {
    if ('range' in patch) setNow(new Date())
    void navigate({ search: (p) => ({ ...p, ...patch }) })
  }
  const rangeSearch = { range: search.range }
  const ranked = (data: { buckets: UsageBucket[] } | undefined, drop = false) => {
    const rows = groupTotals(data?.buckets ?? []).filter((r) => !drop || r.group !== '')
    return { rows: rankByCost(rows, RANK_SIZE), total: knownCost(rows), more: Math.max(0, rows.length - RANK_SIZE) }
  }

  return (
    <section aria-labelledby="view-title" className="dashboard">
      <div className="view-header">
        <h1 id="view-title" tabIndex={-1}>Overview</h1>
        <span className="dim">Updated {now.toLocaleTimeString()}</span>
        <button type="button" onClick={() => setNow(new Date())}>
          Refresh
        </button>
      </div>
      <div className="toolbar">
        <search className="toolbar-search">
          <TimeRangePicker
            value={rangeSearch}
            presets={DASHBOARD_RANGES}
            allowAbsolute={false}
            onChange={(next) => setSearch({ range: next.range as DashboardRange | undefined })}
          />
        </search>
        <Link to="/tools" search={rangeSearch} className="toolbar-link">
          Tool calls
        </Link>
        <Link to="/cost" search={rangeSearch} className="toolbar-link">
          Cost deep-dive
        </Link>
      </div>

      <div className="summary-strip" role="group" aria-label="Headline numbers">
        {current.isError ? (
          <ErrorMessage error={current.error} onRetry={() => void current.refetch()} />
        ) : (
          <Tiles cur={cur} prev={prev} series={series} range={range} costNote={costNote} />
        )}
      </div>

      <div className="dash-grid">
        <Panel
          id="activity-title"
          title="Activity"
          className="span-2"
          action={
            <div className="segmented" role="group" aria-label="Chart metric">
              {(['calls', 'cost'] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  aria-pressed={metric === m}
                  onClick={() => setSearch({ metric: m === 'calls' ? undefined : m })}
                >
                  {m === 'calls' ? 'Calls' : 'Cost'}
                </button>
              ))}
            </div>
          }
        >
          <QueryState query={byModel}>
            {() =>
              cur?.calls === 0 ? (
                <EmptyState>No calls in the last {range}.</EmptyState>
              ) : metric === 'cost' && cur?.cost === null ? (
                <EmptyState>Cost is unknown: no price matched the calls in the last {range}.</EmptyState>
              ) : (
                <ActivityChart
                  slots={slots}
                  series={stack.series}
                  errors={stack.errors}
                  metric={metric}
                  grain={grain}
                  end={Date.parse(window.to)}
                />
              )
            }
          </QueryState>
        </Panel>

        <Panel id="problems-title" title="Problems">
          <QueryState query={byError}>
            {() => {
              const problems = errorGroups(byError.data?.buckets ?? [])
              if (problems.length === 0) {
                return (
                  <p className="state state-empty state-calm">
                    <span aria-hidden="true">✓</span> No errors in the last {range}.
                  </p>
                )
              }
              return (
                <ul className="problems" aria-label="Errors by class">
                  {problems.map((p) => (
                    <li key={p.errorClass}>
                      <Link to="/calls" search={problemSearch(p.errorClass, search.range)}>
                        <span className="badge badge-error">{p.label}</span>
                      </Link>
                      <span className="num">
                        {formatCount(p.calls)} {p.calls === 1 ? 'call' : 'calls'}
                      </span>
                    </li>
                  ))}
                </ul>
              )
            }}
          </QueryState>
        </Panel>

      </div>

      <div className="dash-grid dash-grid-2">
        <Panel
          id="feed-title"
          title="Live feed"
          action={
            <span className="panel-actions">
              {stream.status !== 'off' && (
                <span className="live-status" data-status={stream.status} role="status" aria-live="polite">
                  {STREAM_LABELS[stream.status]}
                </span>
              )}
              {stream.status === 'closed' && (
                <button type="button" onClick={stream.retry}>
                  Retry stream
                </button>
              )}
              <Link to="/calls" search={rangeSearch}>
                Call log
              </Link>
            </span>
          }
        >
          <QueryState query={recent}>
            {() =>
              feed.length === 0 ? (
                <EmptyState>Waiting for calls…</EmptyState>
              ) : (
                <FeedTable calls={feed} passthrough={passthrough} />
              )
            }
          </QueryState>
        </Panel>

        <Panel
          id="sessions-title"
          title="Recent sessions"
          action={
            <Link to="/sessions" search={rangeSearch}>
              All sessions
            </Link>
          }
        >
          <QueryState query={sessions}>
            {() =>
              (sessions.data?.sessions.length ?? 0) === 0 ? (
                <EmptyState>No sessions in the last {range}.</EmptyState>
              ) : (
                <div className="table-frame">
                  <table className="grid-table">
                    <caption className="sr-only">Recent sessions</caption>
                    <thead>
                      <tr>
                        <th scope="col">Session</th>
                        <th scope="col" className="num">Duration</th>
                        <th scope="col" className="num">Calls</th>
                        <th scope="col" className="num">Cost</th>
                        <th scope="col" className="num">Errors</th>
                      </tr>
                    </thead>
                    <tbody>
                      {sessions.data!.sessions.map((s) => (
                        <tr key={s.session_key}>
                          <td className="cell-clip">
                            <Link to="/sessions/$key" params={{ key: s.session_key }} search={{}} className="mono">
                              {s.session_key}
                            </Link>
                          </td>
                          <td className="num">
                            <Maybe text={formatDurationMs(sessionDurationMs(s))} />
                          </td>
                          <td className="num">{formatCount(s.calls)}</td>
                          <td className="num">
                            <Maybe text={formatCostMicroUsd(s.cost_micro_usd)} />
                          </td>
                          <td className="num">
                            {s.errors > 0 ? (
                              <span className="badge badge-error">{`${formatCount(s.errors)} ${s.errors === 1 ? 'error' : 'errors'}`}</span>
                            ) : (
                              <span className="dim">none</span>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )
            }
          </QueryState>
        </Panel>
      </div>

      <section className="breakdowns" aria-labelledby="breakdowns-title">
        <div className="panel-head">
          <h2 id="breakdowns-title">Cost breakdowns</h2>
          {costNote && <small className="dim">Includes API-equivalent cost of client_passthrough providers.</small>}
        </div>
        <div className="dash-grid dash-grid-4">
          {(
            [
              ['model', 'By model', byModel],
              ['client', 'By client', byClient],
              ['key', 'By key', byKey],
            ] as const
          ).map(([group, title, query]) => (
            <Panel
              key={group}
              id={`by-${group}-title`}
              title={title}
              action={
                <Link to="/cost" search={{ ...rangeSearch, group_by: group }}>
                  Details
                </Link>
              }
            >
              <QueryState query={query}>
                {() => {
                  const r = ranked(query.data)
                  return (
                    <>
                      <RankedList label={`Cost ${title.toLowerCase()}`} rows={r.rows} total={r.total} />
                      {r.more > 0 && <p className="notice">{`${formatCount(r.more)} more`}</p>}
                    </>
                  )
                }}
              </QueryState>
            </Panel>
          ))}
          <Panel id="top-sessions-title" title="Top sessions">
            <QueryState query={bySession}>
              {() => {
                const r = ranked(bySession.data, true)
                return (
                  <RankedList
                    label="Top 5 sessions by cost"
                    rows={r.rows}
                    total={r.total}
                    link={(key) => (
                      <Link to="/sessions/$key" params={{ key }} search={{}} className="mono">
                        {key}
                      </Link>
                    )}
                  />
                )
              }}
            </QueryState>
          </Panel>
        </div>
      </section>
    </section>
  )
}

function FeedTable({ calls, passthrough }: { calls: CallSummary[]; passthrough: Set<string> }) {
  return (
    <div className="table-frame">
      <table className="grid-table feed">
        <caption className="sr-only">Latest finished calls</caption>
        <thead>
          <tr>
            <th scope="col">Time</th>
            <th scope="col">Model</th>
            <th scope="col">Status</th>
            <th scope="col" className="num">Tokens in / out</th>
            <th scope="col" className="num">Cost</th>
          </tr>
        </thead>
        <tbody>
          {calls.map((c) => (
            <tr key={c.id}>
              <td>
                <Link to="/calls/$id" params={{ id: c.id }} search={{}} className="num">
                  <time dateTime={c.ts}>{new Date(c.ts).toLocaleTimeString()}</time>
                </Link>
              </td>
              <td className="cell-clip">
                <Tag text={formatText(c.model)} className="mono" />
              </td>
              <td>
                <StatusBadge status={c.status} errorClass={c.error_class} />
              </td>
              <td className="num">
                <Maybe text={formatCount(zeroAsUnknown(tokensIn(c)))} />
                <span className="dim"> / </span>
                <Maybe text={formatCount(zeroAsUnknown(c.usage.output))} />
              </td>
              <td className="num">
                <Maybe text={formatCostMicroUsd(c.cost_micro_usd)} />
                {c.cost_micro_usd !== null && passthrough.has(c.provider) && (
                  <>
                    {' '}
                    <InfoNote label="API-equivalent" note={PASSTHROUGH_NOTE} />
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
