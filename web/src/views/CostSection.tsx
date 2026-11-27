import { useQuery } from '@tanstack/react-query'
import type { UseQueryResult } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useMemo } from 'react'
import type { ReactNode } from 'react'
import { readQueryOptions } from '../api/queries'
import { InfoNote } from '../components/InfoNote'
import { Maybe } from '../components/Maybe'
import { SessionCostTable, ToolCostTable } from '../components/CostTables'
import { MonthlyCostChart } from '../components/MonthlyCostChart'
import { Panel, QueryState } from '../components/Panel'
import { Punchcard } from '../components/Punchcard'
import { RankedList } from '../components/RankedList'
import { EmptyState, ErrorMessage } from '../components/states'
import { formatCostMicroUsd, formatCount, PASSTHROUGH_NOTE } from '../lib/format'
import { COST_RANGES } from '../search/routes'
import type { CostRange } from '../search/routes'
import {
  allCostsUnknown,
  averages,
  browserZone,
  costWindow,
  formatShare,
  knownTotal,
  monthLabel,
  monthWindow,
  mostCostlyMonth,
  mostCostlyProject,
  projectRows,
  runRate,
} from './costOverviewData'
import type { Leader } from './costOverviewData'
import { formatChange } from './dashboardData'

const PROJECT_COUNT = 10
const SESSION_COUNT = 10
const TOOL_COUNT = 10
// The five aggregates are the heaviest queries of the page: keep an answer for a minute (ADR 0059).
const STALE_MS = 60_000

const PERIOD_LABEL: Record<CostRange, string> = { '3m': '3 months', '6m': '6 months', '12m': '12 months' }

type Settled = Pick<UseQueryResult<unknown, unknown>, 'isPending' | 'isError' | 'error' | 'refetch'>

/**
 * A tile that waits for the queries it needs and fails on its own. A tile
 * shows an error only for the queries in `needs`; the ones in `also` improve
 * it when they are there (the share of a project needs the months total).
 */
function Tile({
  label,
  needs,
  also = [],
  children,
}: {
  label: string
  needs: Settled[]
  also?: Settled[]
  children: () => ReactNode
}) {
  const failed = needs.find((q) => q.isError)
  const pending = [...needs, ...also].some((q) => q.isPending)
  return (
    <div className="kpi">
      <dt>{label}</dt>
      <dd>
        {failed ? (
          <ErrorMessage error={failed.error} onRetry={() => void failed.refetch()} />
        ) : pending ? (
          <span className="kpi-value">…</span>
        ) : (
          children()
        )}
      </dd>
    </div>
  )
}

/** The name, cost, share and link of a leader, or the words for a missing one. */
function LeaderValue<T>({
  leader,
  name,
  cost,
  link,
  none = 'no data',
}: {
  leader: Leader<T>
  name: (row: T) => string
  cost: (row: T) => number | null
  link: (row: T) => ReactNode
  none?: string
}) {
  if (leader.kind === 'none') return <span className="kpi-value unknown">{none}</span>
  if (leader.kind === 'unknown') return <span className="kpi-value unknown">unknown</span>
  return (
    <>
      <span className="kpi-value">{name(leader.row)}</span>
      <span className="kpi-sub">
        <Maybe text={formatCostMicroUsd(cost(leader.row))} />
        {leader.share !== null && <span className="dim">{` · ${formatShare(leader.share)} of the period`}</span>}
        {leader.partial && <span className="dim"> · partial</span>}
      </span>
      <small className="kpi-note">{link(leader.row)}</small>
    </>
  )
}

/**
 * The cost section of the overview (ADR 0059): months and projects over a
 * period of whole calendar months in the browser's zone, with its own period
 * control. Every panel is its own query, so one failure or one slow
 * aggregate leaves the others as they are. A panel decides its own empty and
 * "unknown" states from its own data, never from a query it does not need.
 */
export function CostSection({
  range,
  onRangeChange,
  now,
  ready,
}: {
  range: CostRange
  onRangeChange: (next: CostRange) => void
  now: Date
  /** The headline tiles have rendered: the section asks for its data after the first paint. */
  ready: boolean
}) {
  const tz = useMemo(() => browserZone(), [])
  const win = useMemo(() => costWindow(range, now), [range, now])
  const read = { staleTime: STALE_MS, enabled: ready }
  const months = useQuery({ ...readQueryOptions('/admin/usage/months', { query: { ...win, tz } }), ...read })
  const projects = useQuery({
    ...readQueryOptions('/admin/usage/projects', { query: { ...win, limit: PROJECT_COUNT } }),
    ...read,
  })
  const punchcard = useQuery({ ...readQueryOptions('/admin/usage/punchcard', { query: { ...win, tz } }), ...read })
  // The average-per-session tile reads the session count of the same answer.
  const sessions = useQuery({ ...readQueryOptions('/admin/usage/sessions', { query: { ...win, limit: SESSION_COUNT } }), ...read })
  const tools = useQuery({ ...readQueryOptions('/admin/usage/tools', { query: { ...win, limit: TOOL_COUNT } }), ...read })

  const monthRows = useMemo(() => months.data?.months ?? [], [months.data])
  const periodCost = knownTotal(monthRows)
  const periodCalls = monthRows.reduce((n, m) => n + m.calls, 0)
  const best = useMemo(() => mostCostlyMonth(monthRows), [monthRows])
  const pace = useMemo(() => runRate(monthRows, now), [monthRows, now])
  const average = useMemo(
    () => averages(monthRows, sessions.data?.session_count ?? 0),
    [monthRows, sessions.data],
  )
  const projectLeader = useMemo(
    () => (projects.data ? mostCostlyProject(projects.data, periodCost) : undefined),
    [projects.data, periodCost],
  )
  const apiEquivalent =
    monthRows.some((m) => m.api_equivalent) ||
    (projects.data?.projects.some((p) => p.api_equivalent) ?? false) ||
    (punchcard.data?.cells.some((c) => c.api_equivalent) ?? false) ||
    (sessions.data?.sessions.some((x) => x.api_equivalent) ?? false) ||
    (tools.data?.tools.some((t) => t.api_equivalent) ?? false)
  const period = PERIOD_LABEL[range]
  const windowSearch = { from: win.from, to: win.to }
  const noCalls = `No calls in the last ${period}.`
  const allUnknown = (
    <EmptyState>
      Cost is unknown: no price matched the calls in this period. <Link to="/admin/config">Check the pricing in the config</Link>.
    </EmptyState>
  )

  return (
    <section className="cost-section" aria-labelledby="cost-title">
      <header className="cost-head">
        <div className="cost-heading">
          <h2 id="cost-title">Cost</h2>
          <p className="cost-scope">
            <span className="cost-scope-mark">Own period</span>
            The overview time range above does not apply to this section.
          </p>
          <small className="dim">{`Months and hours in ${tz}`}</small>
        </div>
        <div className="cost-period">
          <span className="cost-period-label" aria-hidden="true">Period</span>
          <div className="segmented" role="group" aria-label="Cost period">
            {COST_RANGES.map((r) => (
              <button key={r} type="button" aria-pressed={range === r} onClick={() => onRangeChange(r)}>
                {PERIOD_LABEL[r]}
              </button>
            ))}
          </div>
          {range !== '3m' && <small className="dim">A longer period can take longer to load.</small>}
        </div>
      </header>
      <p className="sr-only" role="status">{`Showing the last ${period}`}</p>
      {apiEquivalent && <p className="notice cost-note">Includes API-equivalent cost of client_passthrough providers. {PASSTHROUGH_NOTE}.</p>}

      <dl className="kpi-grid cost-tiles">
        <Tile label="Most costly month" needs={[months]}>
          {() => (
            <LeaderValue
              leader={best}
              name={(m) => monthLabel(m.month, 'long')}
              cost={(m) => m.cost_micro_usd}
              link={(m) => (
                <Link to="/cost" search={monthWindow(m, now)}>
                  Cost breakdown
                </Link>
              )}
            />
          )}
        </Tile>
        <Tile label="Most costly project" needs={[projects]} also={[months]}>
          {() => {
            const leader = projectLeader ?? ({ kind: 'none' } as const)
            const unassigned = projects.data?.unassigned.calls ?? 0
            return (
              <LeaderValue
                leader={leader}
                none={unassigned > 0 ? 'no project recorded' : 'no data'}
                name={(p) => p.project}
                cost={(p) => p.cost_micro_usd}
                link={(p) => (
                  <Link to="/calls" search={{ project: [p.project], ...windowSearch }}>
                    Call log
                  </Link>
                )}
              />
            )
          }}
        </Tile>
        <Tile label="This month at the current pace" needs={[months]}>
          {() => {
            if (pace === null || periodCalls === 0) return <span className="kpi-value unknown">no data</span>
            return (
              <>
                <span className="kpi-value">
                  {pace.toDate === null ? <span className="unknown">unknown</span> : pace.projected === null ? <span className="unknown">too early</span> : formatCostMicroUsd(pace.projected)}
                </span>
                <span className="kpi-sub">
                  <Maybe text={formatCostMicroUsd(pace.toDate)} />
                  <span className="dim">{` so far in ${monthLabel(pace.month, 'long')}`}</span>
                  {pace.unpriced > 0 && pace.toDate !== null && <span className="dim"> · partial</span>}
                </span>
                {pace.change && pace.previous && pace.change.kind !== 'unknown' && (
                  <span className="change" data-tone={pace.change.kind === 'up' ? 'bad' : 'neutral'}>
                    {(pace.change.kind === 'up' || pace.change.kind === 'down') && (
                      <span aria-hidden="true">{pace.change.kind === 'up' ? '▲ ' : '▼ '}</span>
                    )}
                    {formatChange(pace.change, 'relative')}
                    <span className="dim">{` vs ${monthLabel(pace.previous.month, 'long')}`}</span>
                  </span>
                )}
                <small className="kpi-note">A straight line from the cost so far; it ignores weekday patterns.</small>
              </>
            )
          }}
        </Tile>
        <Tile label="Average cost per session" needs={[months, sessions]}>
          {() => {
            if (periodCalls === 0) return <span className="kpi-value unknown">no data</span>
            return (
              <>
                <span className="kpi-value">
                  <Maybe text={formatCostMicroUsd(average.perSession)} />
                </span>
                <span className="kpi-sub">
                  <Maybe text={formatCostMicroUsd(average.perCall)} />
                  <span className="dim"> per call</span>
                  {average.unpriced > 0 && average.perSession !== null && (
                    <span className="dim">{` · partial: ${formatCount(average.unpriced)} unpriced`}</span>
                  )}
                </span>
                <small className="kpi-note">
                  <InfoNote
                    label="how it is counted"
                    note="Known cost of all calls in the period, divided by the number of sessions. A call with no session key adds to the cost and to no session. Calls with no cost are left out."
                  />
                </small>
              </>
            )
          }}
        </Tile>
      </dl>

      <div className="dash-grid">
        <Panel id="months-title" title="Spend per month" className="span-2">
          <QueryState query={months}>
            {() =>
              periodCalls === 0 ? (
                <EmptyState>{noCalls}</EmptyState>
              ) : periodCost === null ? (
                allUnknown
              ) : (
                <MonthlyCostChart
                  months={monthRows}
                  best={best.kind === 'known' ? best.row : undefined}
                  total={periodCost}
                />
              )
            }
          </QueryState>
        </Panel>

        <Panel id="projects-title" title="Spend per project">
          <QueryState query={projects}>
            {() => {
              const data = projects.data!
              const { unassigned } = data
              if (data.projects.length === 0 && unassigned.calls === 0) return <EmptyState>{noCalls}</EmptyState>
              if (data.projects.length === 0) {
                return (
                  <>
                    <EmptyState>No calls have a project yet.</EmptyState>
                    <p className="notice">
                      A new call takes its project from the <code>x-spyglass-project</code> header, else from the project of its key (<code>keys[].project</code> in the config file), else from the working directory Claude Code reports. Older calls get one only from the backfill. See the runbook, section Projects (docs/runbook.md).
                    </p>
                    <UnassignedRow unassigned={unassigned} />
                  </>
                )
              }
              if (
                periodCost === null &&
                (unassigned.calls === 0 || unassigned.cost_micro_usd === null) &&
                data.projects.every((p) => p.cost_micro_usd === null)
              )
                return allUnknown
              const rows = projectRows(data)
              return (
                <>
                  <RankedList
                    label="Cost per project"
                    rows={rows}
                    total={periodCost ?? 0}
                    showPartial
                    link={(project) => (
                      <Link to="/calls" search={{ project: [project], ...windowSearch }} className="mono">
                        {project}
                      </Link>
                    )}
                  />
                  {data.project_count > data.projects.length && (
                    <p className="notice">{`${formatCount(data.project_count - data.projects.length)} more`}</p>
                  )}
                  <UnassignedRow unassigned={unassigned} />
                </>
              )
            }}
          </QueryState>
        </Panel>
      </div>

      <div className="dash-grid">
        <Panel id="punchcard-title" title="Cost by hour of the week" className="span-all">
          <QueryState query={punchcard}>
            {() => {
              const cells = punchcard.data!.cells
              if (cells.length === 0) return <EmptyState>{noCalls}</EmptyState>
              return (
                <>
                  <Punchcard cells={cells} tz={punchcard.data!.tz} />
                  {allCostsUnknown(cells) && (
                    <p className="notice">
                      Cost is unknown: no price matched the calls in this period. <Link to="/admin/config">Check the pricing in the config</Link>.
                    </p>
                  )}
                </>
              )
            }}
          </QueryState>
        </Panel>
      </div>

      <div className="dash-grid dash-grid-2">
        <Panel
          id="costly-sessions-title"
          title="Most costly sessions"
          action={<Link to="/sessions" search={windowSearch}>All sessions</Link>}
        >
          <QueryState query={sessions}>
            {() => {
              const data = sessions.data!
              if (data.sessions.length === 0) return <EmptyState>{`No sessions in the last ${period}.`}</EmptyState>
              if (allCostsUnknown(data.sessions)) return allUnknown
              return (
                <>
                  <SessionCostTable sessions={data.sessions} />
                  {data.session_count > data.sessions.length && (
                    <p className="notice">{`${formatCount(data.session_count - data.sessions.length)} more`}</p>
                  )}
                </>
              )
            }}
          </QueryState>
        </Panel>

        <Panel id="costly-tools-title" title="Most costly tools" action={<Link to="/tools" search={windowSearch}>All tool calls</Link>}>
          <QueryState query={tools}>
            {() => {
              const data = tools.data!
              if (data.tools.length === 0) return <EmptyState>No tool calls in this period.</EmptyState>
              if (allCostsUnknown(data.tools)) return allUnknown
              return (
                <>
                  <ToolCostTable tools={data.tools} window={windowSearch} />
                  {data.tool_count > data.tools.length && (
                    <p className="notice">{`${formatCount(data.tool_count - data.tools.length)} more`}</p>
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

/** The calls with no project: shown after the ranking and never ranked, because it is not a project. */
function UnassignedRow({ unassigned }: { unassigned: { calls: number; cost_micro_usd: number | null; unpriced_calls: number } }) {
  if (unassigned.calls === 0) return null
  return (
    <p className="notice unassigned">
      <span>No project</span>
      {': '}
      <Maybe text={formatCostMicroUsd(unassigned.cost_micro_usd)} />
      {` · ${formatCount(unassigned.calls)} ${unassigned.calls === 1 ? 'call' : 'calls'}`}
      {unassigned.unpriced_calls > 0 && unassigned.cost_micro_usd !== null && ` · partial: ${formatCount(unassigned.unpriced_calls)} unpriced`}
    </p>
  )
}
