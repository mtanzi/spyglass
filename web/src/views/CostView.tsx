import { useQueries, useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { readQueryOptions } from '../api/queries'
import { ActivityChart } from '../components/ActivityChart'
import { ChartTable } from '../components/ChartTable'
import { Tag } from '../components/Badge'
import { RatioChart } from '../components/RatioChart'
import { EmptyState, ErrorMessage, LoadingState } from '../components/states'
import { TimeRangePicker } from '../components/TimeRangePicker'
import { UNKNOWN, formatCostMicroUsd, formatCount, formatText } from '../lib/format'
import { COST_GROUPS, DEFAULT_COST_GROUP } from '../search/routes'
import type { CallsSearch, CostGroup, CostSearch } from '../search/routes'
import { resolveTimeRange } from '../search/timeRange'
import type { TimeRangeSearch } from '../search/timeRange'
import { filterQuery } from './callLogRows'
import {
  GROUP_LABELS,
  TASK_KEY_PREFIX,
  apiEquivalentGroups,
  costGrain,
  costRows,
  costShare,
  knownCost,
  promptCoverage,
  sortByCost,
  taskRows,
  topSessions,
} from './costData'
import type { CostRow } from './costData'
import { apiEquivalentCost, passthroughProviders, slotSeries, stackByGroup, timeSlots, totals } from './dashboardData'
import { formatRatio } from './SessionList'
import { shortId } from './sessionTree'

const TOP_SESSIONS = 10
const PROMPT_ROWS = 25
const GROUP_ROWS = 50

type UsageOptions = ReturnType<typeof readQueryOptions<'/admin/usage'>>

/** The groups of the given client_passthrough series whose cost is partly API-equivalent. */
function useApiEquivalentGroups(queries: UsageOptions[]): Set<string> {
  return useQueries({
    queries,
    combine: (results) => apiEquivalentGroups(results.flatMap((r) => r.data?.buckets ?? [])),
  })
}

function Maybe({ text }: { text: string }) {
  return text === UNKNOWN ? <span className="unknown">{UNKNOWN}</span> : <>{text}</>
}

function Panel({
  id,
  title,
  note,
  className,
  children,
}: {
  id: string
  title: string
  note?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <section className={className ? `panel ${className}` : 'panel'} aria-labelledby={id}>
      <div className="panel-head">
        <h2 id={id}>{title}</h2>
        {note && <small className="dim">{note}</small>}
      </div>
      {children}
    </section>
  )
}

/** Loading, error and content of a panel from one query. A long range can take seconds. */
function QueryState({
  query,
  label,
  children,
}: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown }
  label?: string
  children: () => ReactNode
}) {
  if (query.isError) return <ErrorMessage error={query.error} onRetry={() => void query.refetch()} />
  if (query.isPending) return <LoadingState label={label} />
  return <>{children()}</>
}

/** A cost with the unknown and API-equivalent cases the ADR asks for. */
function CostCell({ cost, unpriced, apiEquivalent }: { cost: number | null; unpriced: number; apiEquivalent?: boolean }) {
  return (
    <>
      <Maybe text={formatCostMicroUsd(cost)} />
      {cost !== null && apiEquivalent && (
        <small className="dim" title="client_passthrough: the plan bills the person, not per token">
          {' '}
          API-equivalent
        </small>
      )}
      {cost !== null && unpriced > 0 && (
        <small className="dim" title="Calls with no price are left out of the cost">
          {' '}
          + {formatCount(unpriced)} unpriced
        </small>
      )}
    </>
  )
}

/** How a group value links to the view that shows its calls. */
function GroupLink({ group, value }: { group: CostGroup; value: string }) {
  if (value === '') return <span className="unknown">{UNKNOWN}</span>
  if (group === 'session') {
    return (
      <Link to="/sessions/$key" params={{ key: value }} search={{}} className="mono">
        {value}
      </Link>
    )
  }
  return (
    <Link to="/calls" search={{ [group]: [value] }} className="mono">
      {value}
    </Link>
  )
}

function GroupTable({
  group,
  rows,
  total,
  apiEquivalent,
}: {
  group: CostGroup
  rows: CostRow[]
  total: number
  /** The groups whose cost is partly API-equivalent. */
  apiEquivalent: Set<string>
}) {
  if (rows.length === 0) return <EmptyState>No calls in this range.</EmptyState>
  const shown = rows.slice(0, GROUP_ROWS)
  return (
    <>
      <div className="table-frame">
        <table className="grid-table">
          <caption className="sr-only">{`Cost by ${GROUP_LABELS[group]}`}</caption>
          <thead>
            <tr>
              <th scope="col">{GROUP_LABELS[group]}</th>
              <th scope="col" className="num">Calls</th>
              <th scope="col" className="num">Cost</th>
              <th scope="col" className="num">Share</th>
              <th scope="col" className="num">Cache hit</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => {
              const share = costShare(r.cost, total)
              return (
                <tr key={r.group}>
                  <th scope="row" className="cell-clip">
                    <GroupLink group={group} value={r.group} />
                  </th>
                  <td className="num">{formatCount(r.calls)}</td>
                  <td className="num">
                    <CostCell cost={r.cost} unpriced={r.unpriced} apiEquivalent={apiEquivalent.has(r.group)} />
                  </td>
                  <td className="num">{share === null ? <Maybe text={UNKNOWN} /> : `${share}%`}</td>
                  <td className="num">
                    <Maybe text={formatRatio(r.cacheHit)} />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {rows.length > shown.length && <p className="notice">{`${formatCount(rows.length - shown.length)} more not shown`}</p>}
    </>
  )
}

function ActiveFilters({ search }: { search: CostSearch }) {
  const filters = Object.entries(filterQuery(search as CallsSearch)).filter(([, v]) => v !== undefined)
  if (filters.length === 0) return null
  return (
    <p className="notice" aria-label="Active filters">
      Filtered by {filters.map(([k, v]) => `${k} = ${String(v)}`).join(', ')}.{' '}
      <Link to="/cost" search={{ range: search.range, from: search.from, to: search.to, group_by: search.group_by }}>
        Clear filters
      </Link>
    </p>
  )
}

/**
 * Cost and cache (ADR 0035): who spent what, by key, model, client and
 * session; the top sessions; cost per firstmate task and per prompt; the
 * cache hit ratio over time. State is in the URL: range, grouping, grain and
 * the call-log filters.
 */
export function CostView() {
  const search = useSearch({ from: '/cost' }) as CostSearch
  const navigate = useNavigate({ from: '/cost' })
  const group: CostGroup = search.group_by ?? DEFAULT_COST_GROUP

  // The window is fixed per search and `now`, so the query keys are stable until Refresh.
  const [now, setNow] = useState(() => new Date())
  const timeKey = `${search.range ?? ''}|${search.from ?? ''}|${search.to ?? ''}`
  const window = useMemo(
    () => resolveTimeRange({ range: search.range, from: search.from, to: search.to }, now),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [timeKey, now],
  )
  const grain = costGrain(window, search.grain)
  const slots = useMemo(() => timeSlots(window, grain), [window, grain])
  const filterKey = JSON.stringify(filterQuery(search as CallsSearch))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const filters = useMemo(() => filterQuery(search as CallsSearch), [filterKey])

  const usage = (extra: { group_by?: CostGroup | 'provider'; grain: typeof grain; provider?: string[] }) =>
    readQueryOptions('/admin/usage', { query: { ...filters, ...window, ...extra } })
  const overall = useQuery(usage({ grain }))
  // The series per interval, grouped by the chosen dimension.
  const series = useQuery(usage({ grain, group_by: group }))
  // The tables sum over the range, so the widest grain keeps them small.
  const table = useQuery(usage({ grain: 'day', group_by: group }))
  const bySession = useQuery(usage({ grain: 'day', group_by: 'session' }))
  const byKey = useQuery(usage({ grain: 'day', group_by: 'key' }))
  const config = useQuery(readQueryOptions('/admin/config'))
  const passthrough = useMemo(() => passthroughProviders(config.data?.providers), [config.data])
  // Only a client_passthrough provider makes part of the cost API-equivalent.
  const byProvider = useQuery({ ...usage({ grain: 'day', group_by: 'provider' }), enabled: passthrough.size > 0 })
  // One series per client_passthrough provider marks the groups with API-equivalent cost.
  const passthroughNames = [...passthrough].filter((p) => !filters.provider || filters.provider.includes(p))
  const passthroughBy = (g: CostGroup) => passthroughNames.map((p) => usage({ grain: 'day', group_by: g, provider: [p] }))
  const apiEquivalentRows = useApiEquivalentGroups(passthroughBy(group))
  const apiEquivalentSessions = useApiEquivalentGroups(passthroughBy('session'))
  const apiEquivalentKeys = useApiEquivalentGroups(passthroughBy('key'))
  // The server ranks every prompt of the range and returns the most costly ones.
  const prompts = useQuery(readQueryOptions('/admin/usage/prompts', { query: { ...filters, ...window, limit: PROMPT_ROWS } }))

  const sum = useMemo(() => (overall.data ? totals(overall.data.buckets) : undefined), [overall.data])
  const stack = useMemo(() => stackByGroup(series.data?.buckets ?? [], slots, 'cost'), [series.data, slots])
  const cacheValues = useMemo(() => slotSeries(overall.data?.buckets ?? [], slots, 'cacheHit'), [overall.data, slots])
  const rows = useMemo(() => sortByCost(costRows(table.data?.buckets ?? [])), [table.data])
  const apiEquivalent = byProvider.data ? apiEquivalentCost(byProvider.data.buckets, passthrough) : 0
  const costNote =
    apiEquivalent <= 0 || sum?.cost == null
      ? undefined
      : apiEquivalent >= sum.cost
        ? 'API-equivalent'
        : `incl. ${formatCostMicroUsd(apiEquivalent)} API-equivalent`

  const setSearch = (patch: Partial<CostSearch>) => {
    if ('range' in patch || 'from' in patch || 'to' in patch) setNow(new Date())
    void navigate({ search: (p) => ({ ...p, ...patch }) })
  }
  const timeValue: TimeRangeSearch = { range: search.range, from: search.from, to: search.to }
  const rangeText = search.from || search.to ? 'this range' : `the last ${search.range ?? '24h'}`

  return (
    <section aria-labelledby="view-title" className="dashboard cost-view">
      <div className="view-header">
        <h1 id="view-title">Cost</h1>
        <span className="dim">Updated {now.toLocaleTimeString()}</span>
        <button type="button" onClick={() => setNow(new Date())}>
          Refresh
        </button>
      </div>
      <div className="toolbar">
        <TimeRangePicker value={timeValue} onChange={(next) => setSearch(next)} />
        <div className="segmented" role="group" aria-label="Group by">
          {COST_GROUPS.map((g) => (
            <button
              key={g}
              type="button"
              aria-pressed={group === g}
              onClick={() => setSearch({ group_by: g === DEFAULT_COST_GROUP ? undefined : g })}
            >
              {GROUP_LABELS[g]}
            </button>
          ))}
        </div>
        <Link to="/" search={{ range: undefined }} className="toolbar-link">
          Overview
        </Link>
      </div>
      <ActiveFilters search={search} />
      {costNote && (
        <p className="notice">
          Cost includes the API-equivalent cost of client_passthrough providers: the plan bills the person, not per token.
        </p>
      )}

      <div className="summary-strip" role="group" aria-label="Cost totals">
        {overall.isError ? (
          <ErrorMessage error={overall.error} onRetry={() => void overall.refetch()} />
        ) : (
          <dl className="kpi-grid kpi-grid-4">
            <div className="kpi">
              <dt>Cost</dt>
              <dd>
                <span className="kpi-value">{sum ? <Maybe text={formatCostMicroUsd(sum.cost)} /> : '…'}</span>
                {sum && costNote && <small className="kpi-note">{costNote}</small>}
              </dd>
            </div>
            <div className="kpi">
              <dt>Calls</dt>
              <dd>
                <span className="kpi-value">{sum ? formatCount(sum.calls) : '…'}</span>
              </dd>
            </div>
            <div className="kpi">
              <dt>Unpriced calls</dt>
              <dd>
                <span className="kpi-value">{sum ? formatCount(sum.unpriced) : '…'}</span>
                {sum && sum.unpriced > 0 && <small className="kpi-note">no price entry; left out of the cost</small>}
              </dd>
            </div>
            <div className="kpi">
              <dt>Cache hit</dt>
              <dd>
                <span className="kpi-value">{sum ? <Maybe text={formatRatio(sum.cacheHit)} /> : '…'}</span>
              </dd>
            </div>
          </dl>
        )}
      </div>

      <div className="dash-grid">
        <Panel id="cost-time-title" title={`Cost over time by ${GROUP_LABELS[group]}`} className="span-2">
          <QueryState query={series} label="Loading cost (a long range can take a few seconds)">
            {() =>
              sum?.calls === 0 ? (
                <EmptyState>No calls in {rangeText}.</EmptyState>
              ) : sum?.cost === null ? (
                <EmptyState>Cost is unknown: no price matched the calls in {rangeText}.</EmptyState>
              ) : (
                <>
                  <ActivityChart
                    slots={slots}
                    series={stack.series}
                    errors={stack.errors}
                    metric="cost"
                    grain={grain}
                    end={Date.parse(window.to)}
                    dimension={GROUP_LABELS[group]}
                    showErrors={false}
                  />
                  <ChartTable
                    caption={`Cost per ${grain} by ${GROUP_LABELS[group]}`}
                    slots={slots}
                    series={stack.series}
                    grain={grain}
                    format={(v) => formatCostMicroUsd(v)}
                    total={(values) => formatCostMicroUsd(values.reduce<number>((a, b) => a + (b ?? 0), 0))}
                  />
                </>
              )
            }
          </QueryState>
        </Panel>

        <Panel id="cache-title" title="Cache hit ratio over time" note="cache read over all input tokens">
          <QueryState query={overall} label="Loading cache hit ratio">
            {() =>
              cacheValues.every((v) => v === null) ? (
                <EmptyState>No input tokens in {rangeText}, so there is no cache hit ratio.</EmptyState>
              ) : (
                <>
                  <RatioChart
                    slots={slots}
                    values={cacheValues}
                    grain={grain}
                    end={Date.parse(window.to)}
                    label="Cache hit ratio"
                  />
                  <ChartTable
                    caption={`Cache hit ratio per ${grain}`}
                    slots={slots}
                    series={[{ name: 'Cache hit', values: cacheValues }]}
                    grain={grain}
                    format={(v) => formatRatio(v)}
                    keepZero
                  />
                </>
              )
            }
          </QueryState>
        </Panel>
      </div>

      <div className="dash-grid dash-grid-2">
        <Panel id="by-group-title" title={`Cost by ${GROUP_LABELS[group]}`}>
          <QueryState query={table} label="Loading cost (a long range can take a few seconds)">
            {() => <GroupTable group={group} rows={rows} total={knownCost(rows)} apiEquivalent={apiEquivalentRows} />}
          </QueryState>
        </Panel>

        <Panel id="top-sessions-title" title="Top sessions by cost">
          <QueryState query={bySession} label="Loading sessions">
            {() => {
              const all = costRows(bySession.data?.buckets ?? [])
              const top = topSessions(all, TOP_SESSIONS)
              const total = knownCost(all.filter((r) => r.group !== ''))
              if (top.length === 0) return <EmptyState>No sessions in {rangeText}.</EmptyState>
              return <GroupTable group="session" rows={top} total={total} apiEquivalent={apiEquivalentSessions} />
            }}
          </QueryState>
        </Panel>
      </div>

      <div className="dash-grid dash-grid-2">
        <Panel
          id="tasks-title"
          title="Cost per firstmate task"
          note={`keys named ${TASK_KEY_PREFIX}<task-id>`}
        >
          <QueryState query={byKey} label="Loading keys">
            {() => {
              const tasks = taskRows(costRows(byKey.data?.buckets ?? []))
              if (tasks.length === 0) {
                return <EmptyState>{`No ${TASK_KEY_PREFIX}<task-id> keys made calls in ${rangeText}.`}</EmptyState>
              }
              const total = knownCost(tasks)
              return (
                <div className="table-frame">
                  <table className="grid-table">
                    <caption className="sr-only">Cost per firstmate task</caption>
                    <thead>
                      <tr>
                        <th scope="col">Task</th>
                        <th scope="col" className="num">Calls</th>
                        <th scope="col" className="num">Cost</th>
                        <th scope="col" className="num">Share</th>
                        <th scope="col" className="num">Cache hit</th>
                      </tr>
                    </thead>
                    <tbody>
                      {tasks.map((t) => {
                        const share = costShare(t.cost, total)
                        return (
                          <tr key={t.group}>
                            <th scope="row" className="cell-clip">
                              <Link to="/calls" search={{ key: [t.group] }} className="mono" title={t.group}>
                                {t.taskId}
                              </Link>
                            </th>
                            <td className="num">{formatCount(t.calls)}</td>
                            <td className="num">
                              <CostCell cost={t.cost} unpriced={t.unpriced} apiEquivalent={apiEquivalentKeys.has(t.group)} />
                            </td>
                            <td className="num">{share === null ? <Maybe text={UNKNOWN} /> : `${share}%`}</td>
                            <td className="num">
                              <Maybe text={formatRatio(t.cacheHit)} />
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              )
            }}
          </QueryState>
        </Panel>

        <Panel
          id="prompts-title"
          title="Cost per prompt"
          note={prompts.data ? `${formatCount(prompts.data.prompts.length)} of ${formatCount(prompts.data.prompt_count)} prompts` : undefined}
        >
          <QueryState query={prompts} label="Loading prompts">
            {() => {
              const data = prompts.data
              if (!data || data.prompts.length === 0) {
                return <EmptyState>No calls with a prompt id in {rangeText}.</EmptyState>
              }
              return (
                <>
                  <div className="table-frame">
                    <table className="grid-table">
                      <caption className="sr-only">Cost per prompt</caption>
                      <thead>
                        <tr>
                          <th scope="col">Prompt</th>
                          <th scope="col">Session</th>
                          <th scope="col" className="num">Calls</th>
                          <th scope="col" className="num">Cost</th>
                        </tr>
                      </thead>
                      <tbody>
                        {data.prompts.map((p) => (
                          <tr key={p.prompt_id}>
                            <th scope="row" className="cell-clip">
                              <Link to="/calls" search={{ prompt: [p.prompt_id] }} className="mono" title={p.prompt_id}>
                                {shortId(p.prompt_id)}
                              </Link>
                            </th>
                            <td className="cell-clip">
                              {p.session_key === '' ? (
                                <Tag text={formatText(p.session_key)} />
                              ) : (
                                <Link to="/sessions/$key" params={{ key: p.session_key }} search={{}} className="mono">
                                  {p.session_key}
                                </Link>
                              )}
                            </td>
                            <td className="num">{formatCount(p.calls)}</td>
                            <td className="num">
                              <CostCell cost={p.cost_micro_usd} unpriced={p.unpriced_calls} apiEquivalent={p.api_equivalent} />
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  {promptCoverage(data.prompts.length, data.prompt_count) && (
                    <p className="notice">{promptCoverage(data.prompts.length, data.prompt_count)}</p>
                  )}
                  {data.calls_without_prompt > 0 && (
                    <p className="notice">{`${formatCount(data.calls_without_prompt)} calls have no prompt id and are not counted here.`}</p>
                  )}
                </>
              )
            }}
          </QueryState>
        </Panel>
      </div>
    </section>
  )
}
