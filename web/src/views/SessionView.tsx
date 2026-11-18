import { useQuery } from '@tanstack/react-query'
import { useRem } from '../lib/rem'
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useEffect, useMemo, useRef, useState } from 'react'
import { apiGet } from '../api/client'
import type { QueryOf } from '../api/client'
import { isRetryable } from '../api/errors'
import { queryKey } from '../api/keys'
import { readQueryOptions } from '../api/queries'
import { ContentNotStored } from '../components/ContentNotStored'
import { EmptyState, ErrorMessage, LoadingState } from '../components/states'
import { useRowNavigation } from '../components/useRowNavigation'
import {
  UNKNOWN,
  formatCostMicroUsd,
  formatCount,
  formatDurationMs,
  formatText,
  formatTimeSplit,
} from '../lib/format'
import { SESSION_MODES } from '../search/routes'
import type { SessionMode } from '../search/routes'
import { cacheHitRatio, formatRatio } from './SessionList'
import {
  agentLabel,
  buildLanes,
  buildSessionModel,
  countSummary,
  defaultExpanded,
  flattenTree,
  shortId,
} from './sessionTree'
import type { FlatRow, LaneRow, SessionDetail, SessionModel, ToolCall, TreeNode } from './sessionTree'
import type { TimeAxis } from './timeAxis'

const MODE_LABELS: Record<SessionMode, string> = { tree: 'Tree', time: 'Time' }
const TOOL_PAGE = 500
/** A tool use is timed at the end of its response; leave room past the last call start. */
const TOOL_WINDOW_SLACK_MS = 60_000

/** Window of the session's tool-call query: its first call start to past its last call end. */
export function sessionToolWindow(detail: SessionDetail): { from: string; to: string } | null {
  if (detail.calls.length === 0) return null
  let first = Infinity
  let last = -Infinity
  for (const c of detail.calls) {
    const t = Date.parse(c.ts)
    first = Math.min(first, t)
    last = Math.max(last, t + Math.max(0, c.duration_ms))
  }
  return { from: new Date(first).toISOString(), to: new Date(last + TOOL_WINDOW_SLACK_MS).toISOString() }
}

/** Every tool call of a session: pages through GET /admin/tool-calls?session=. */
export async function fetchSessionToolCalls(
  query: QueryOf<'/admin/tool-calls'>,
  signal?: AbortSignal,
): Promise<ToolCall[]> {
  const out: ToolCall[] = []
  let cursor: string | undefined
  do {
    const page = await apiGet('/admin/tool-calls', { query: { ...query, cursor } }, { signal })
    out.push(...page.tool_calls)
    cursor = page.next_cursor ?? undefined
  } while (cursor !== undefined)
  return out
}

/** V2 session view (ADR 0033): tree and time modes on a shared compressed axis. */
export function SessionView() {
  const { key } = useParams({ from: '/sessions/$key' })
  const { mode = 'tree' } = useSearch({ from: '/sessions/$key' })
  const navigate = useNavigate()
  const detail = useQuery(readQueryOptions('/admin/sessions/{key}', { path: { key } }))

  const window = detail.data ? sessionToolWindow(detail.data) : null
  const toolQuery = window ? { session: key, ...window, limit: TOOL_PAGE } : null
  const tools = useQuery({
    queryKey: ['all-pages', ...queryKey('/admin/tool-calls', { query: toolQuery ?? {} })],
    queryFn: ({ signal }) => fetchSessionToolCalls(toolQuery!, signal),
    enabled: toolQuery !== null,
    staleTime: 0,
    retry: (count, error) => count < 2 && isRetryable(error),
  })

  const model = useMemo(
    () => (detail.data ? buildSessionModel(detail.data, tools.data ?? []) : null),
    [detail.data, tools.data],
  )
  const openCall = (id: string) => void navigate({ to: '/calls/$id', params: { id }, search: {} })

  return (
    <section aria-labelledby="view-title" className="session-view">
      <nav aria-label="Breadcrumb" className="crumbs">
        <Link to="/sessions" search={{}}>
          Sessions
        </Link>
        <span aria-hidden="true">/</span>
        <span className="mono">{key}</span>
      </nav>
      <div className="view-header">
        <h1 id="view-title" tabIndex={-1}>Session</h1>
        <nav aria-label="Session view mode" className="segmented">
          {SESSION_MODES.map((m) => (
            <Link
              key={m}
              to="/sessions/$key"
              params={{ key }}
              search={m === 'tree' ? {} : { mode: m }}
              activeOptions={{ exact: true, includeSearch: true }}
              aria-current={m === mode ? 'page' : undefined}
            >
              {MODE_LABELS[m]}
            </Link>
          ))}
        </nav>
      </div>
      {detail.isPending && <LoadingState label="Loading session" />}
      {detail.isError && <ErrorMessage error={detail.error} onRetry={() => void detail.refetch()} />}
      {detail.data && model && (
        <>
          <SessionSummaryStrip detail={detail.data} model={model} toolCount={tools.data?.length} toolsPending={tools.isPending} />
          {tools.isError && (
            <div className="inline-error">
              <span className="dim">Tool calls could not be loaded.</span>
              <ErrorMessage error={tools.error} onRetry={() => void tools.refetch()} />
            </div>
          )}
          {detail.data.calls.length === 0 ? (
            <EmptyState>This session has no calls in the retention window.</EmptyState>
          ) : mode === 'tree' ? (
            <SessionTree model={model} onOpenCall={openCall} />
          ) : (
            <SessionWaterfall model={model} />
          )}
          <Legend model={model} />
          <ContentNotStored />
        </>
      )}
    </section>
  )
}

function SessionSummaryStrip({
  detail,
  model,
  toolCount,
  toolsPending,
}: {
  detail: SessionDetail
  model: SessionModel
  toolCount: number | undefined
  toolsPending: boolean
}) {
  const s = detail.summary
  const span = model.axis.end - model.axis.start
  const prompts = model.prompts.filter((p) => p.promptId !== '').length
  const items: [string, React.ReactNode][] = [
    ['Start', <time key="t" dateTime={s.first_ts}>{new Date(s.first_ts).toLocaleString()}</time>],
    ['Duration', formatDurationMs(Number.isFinite(span) ? span : null)],
    ['Client', formatText(s.client)],
    ['Key', formatText(s.key)],
    ['Prompts', formatCount(prompts)],
    ['Agents', formatCount(s.agent_count)],
    ['Calls', formatCount(s.calls)],
    ['Tool calls', toolsPending ? '…' : formatCount(toolCount)],
    ['Model / tool time', formatTimeSplit(s.llm_ms, s.tool_ms, s.tool_ms_estimated)],
    ['Cost', formatCostMicroUsd(s.cost_micro_usd)],
    ['Cache hit', formatRatio(cacheHitRatio(s.usage))],
    [
      'Errors',
      s.errors > 0 ? <span key="e" className="badge badge-error">{formatCount(s.errors)}</span> : '0',
    ],
  ]
  return (
    <dl className="meta-strip" aria-label="Session summary">
      {items.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd className={value === UNKNOWN ? 'unknown' : undefined}>{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function Legend({ model }: { model: SessionModel }) {
  return (
    <p className="legend dim">
      <span className="legend-gap" aria-hidden="true" /> idle gap ({formatCount(model.axis.gaps.length)}) collapsed
      <span className="legend-compaction" aria-hidden="true" /> context compacted ({formatCount(model.compactions.length)})
    </p>
  )
}

const pct = (v: number) => `${(v * 100).toFixed(3)}%`

/** Idle-gap markers and compaction lines, drawn behind the bars of every row and on the axis. */
function AxisGuides({ axis, compactions }: { axis: TimeAxis; compactions: number[] }) {
  return (
    <>
      {axis.gaps.map((g) => (
        <span
          key={`g${g.start}`}
          className="axis-gap"
          style={{ left: pct(g.from), width: pct(g.to - g.from) }}
          aria-hidden="true"
        />
      ))}
      {compactions.map((t, i) => (
        <span key={`c${i}`} className="axis-compaction" style={{ left: pct(axis.position(t)) }} aria-hidden="true" />
      ))}
    </>
  )
}

/** Closest two tick labels may sit on the axis, as a share of its width. */
const MIN_TICK_GAP = 0.08

/**
 * Tick labels at the session start and after each collapsed gap, skipping
 * one that would crowd the previous label. Each gap marker shows the idle
 * time it hides.
 */
export function axisTicks(axis: TimeAxis): { at: number; offset: number }[] {
  const ticks: { at: number; offset: number }[] = []
  axis.segments.forEach((s, i) => {
    const opensStretch = !s.collapsed && (i === 0 || axis.segments[i - 1].collapsed)
    const last = ticks[ticks.length - 1]
    if (opensStretch && (!last || s.from - last.at >= MIN_TICK_GAP)) {
      ticks.push({ at: s.from, offset: s.start - axis.start })
    }
  })
  return ticks
}

function AxisHeader({ axis, compactions }: { axis: TimeAxis; compactions: number[] }) {
  const ticks = axisTicks(axis)
  return (
    <div className="axis" aria-hidden="true">
      <AxisGuides axis={axis} compactions={compactions} />
      {ticks.map((t) => (
        <span key={`t${t.at}`} className="axis-tick" style={{ left: pct(t.at) }}>
          +{formatDurationMs(t.offset)}
        </span>
      ))}
      {axis.gaps.map((g) => (
        <span key={`l${g.start}`} className="axis-gap-label" style={{ left: pct(g.from) }} >
          ⋯ {formatDurationMs(g.end - g.start)}
        </span>
      ))}
      {compactions.map((t, i) => (
        <span key={`m${i}`} className="axis-compaction-mark" style={{ left: pct(axis.position(t)) }}>
          ▾
        </span>
      ))}
    </div>
  )
}

function nodeLabel(node: TreeNode, model: SessionModel): { kind: string; title: string; sub: string } {
  switch (node.kind) {
    case 'prompt':
      return {
        kind: 'Prompt',
        title: `Prompt ${node.promptIndex + 1}`,
        sub: node.promptId === '' ? 'no prompt id' : shortId(node.promptId),
      }
    case 'agent':
      return {
        kind: node.parentAgentId === '' ? 'Agent' : 'Sub-agent',
        title: agentLabel(node.agentId, model.agents.get(node.agentId)),
        sub: node.agentId === '' ? '' : shortId(node.agentId),
      }
    case 'call': {
      const c = node.call
      return {
        kind: 'LLM',
        title: formatText(c.model),
        sub: [c.status === 0 ? '' : String(c.status), c.stop_reason].filter((v) => v !== '').join(' · '),
      }
    }
    case 'tool': {
      const t = node.tool
      return {
        kind: 'Tool',
        title: t.name || UNKNOWN,
        sub: [t.status === 'paired' ? '' : t.status.replace('_', ' '), t.duration_source === 'estimated' ? 'estimated' : '']
          .filter((v) => v !== '')
          .join(' · '),
      }
    }
  }
}

function Num({ text }: { text: string }) {
  return <span className={text === UNKNOWN ? 'num unknown' : 'num'}>{text}</span>
}

/** Row heights in rem (28px and 24px at the default text size). */
const TREE_ROW = 1.75

/**
 * Tree mode: prompt, agent, call and tool call as one flattened, virtualised
 * list, each row with its numbers and a bar on the shared axis.
 */
function SessionTree({ model, onOpenCall }: { model: SessionModel; onOpenCall: (id: string) => void }) {
  const rem = useRem()
  const [overrides, setOverrides] = useState<Record<string, boolean>>({})
  const promptCount = model.prompts.length
  const rows = useMemo(
    () => flattenTree(model.prompts, (n) => overrides[n.id] ?? defaultExpanded(n, promptCount)),
    [model, overrides, promptCount],
  )
  const scrollRef = useRef<HTMLDivElement>(null)
  // TanStack Virtual returns unmemoizable functions; the React Compiler skips this component.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    // Rows are measured (a count summary can wrap), so key the size cache by
    // node: when tool calls load or a node collapses the indexes shift, and an
    // index-keyed cache would give a row another row's height.
    getItemKey: (index) => rows[index].node.id,
    estimateSize: () => TREE_ROW * rem,
    overscan: 12,
  })
  // The row height is a count of rem: measure again when the text size changes.
  useEffect(() => {
    virtualizer.measure()
  }, [virtualizer, rem])
  const nav = useRowNavigation({
    count: rows.length,
    containerRef: scrollRef,
    scrollToIndex: (i) => virtualizer.scrollToIndex(i),
  })
  const guides = useMemo(() => <AxisGuides axis={model.axis} compactions={model.compactions} />, [model])

  const toggle = (row: FlatRow, open = !row.expanded) => {
    if (row.hasChildren) setOverrides((o) => ({ ...o, [row.node.id]: open }))
  }
  const activate = (row: FlatRow) => {
    if (row.node.kind === 'call') onOpenCall(row.node.call.id)
    else if (row.node.kind === 'tool') onOpenCall(row.node.callId)
    else toggle(row)
  }
  const onKeyDown = (e: React.KeyboardEvent, row: FlatRow, index: number) => {
    if (nav.onRowKeyDown(e, index)) return
    switch (e.key) {
      case 'ArrowRight':
      case 'l':
        e.preventDefault()
        if (row.hasChildren && !row.expanded) toggle(row, true)
        else if (row.expanded) nav.focusRow(index + 1)
        break
      case 'ArrowLeft':
      case 'h':
        e.preventDefault()
        if (row.expanded) toggle(row, false)
        else if (row.parentId !== null) nav.focusRow(rows.findIndex((r) => r.node.id === row.parentId))
        break
      case 'Enter':
        e.preventDefault()
        activate(row)
        break
      case ' ':
        e.preventDefault()
        toggle(row)
        break
    }
  }

  return (
    <div className="trace">
      <div className="trace-tools">
        <span className="hint dim">
          <kbd>j</kbd> <kbd>k</kbd> move · <kbd>h</kbd> <kbd>l</kbd> collapse, expand · <kbd>Enter</kbd> open call
        </span>
        <span className="hint dim">
          Model / tool time: shares of summed model time and tool time. Parallel tool calls and sub-agents overlap
          in wall time, so these are not shares of Duration. “est.” marks an estimated tool duration.
        </span>
      </div>
      <div className="tree-head" aria-hidden="true">
        <span>Name</span>
        <span className="num">Duration</span>
        <span className="num">Model / tool time</span>
        <span className="num">Tokens</span>
        <span className="num">Cost</span>
        <span className="num">Errors</span>
        <AxisHeader axis={model.axis} compactions={model.compactions} />
      </div>
      <div ref={scrollRef} className="trace-scroll">
        <div role="tree" aria-label="Session tree" style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index]
            const { node } = row
            const label = nodeLabel(node, model)
            const from = model.axis.position(node.start)
            const to = model.axis.position(node.end)
            return (
              <div
                key={node.id}
                role="treeitem"
                data-index={item.index}
                aria-level={row.depth + 1}
                aria-posinset={row.position}
                aria-setsize={row.siblings}
                aria-expanded={row.hasChildren ? row.expanded : undefined}
                aria-label={`${label.kind} ${label.title}`}
                tabIndex={item.index === nav.active ? 0 : -1}
                className={`tree-row kind-${node.kind}${node.errors > 0 ? ' has-error' : ''}`}
                ref={virtualizer.measureElement}
                style={{ minHeight: TREE_ROW * rem, transform: `translateY(${item.start}px)` }}
                onFocus={() => nav.setActive(item.index)}
                onClick={() => {
                  nav.setActive(item.index)
                  activate(row)
                }}
                onKeyDown={(e) => onKeyDown(e, row, item.index)}
              >
                <span className="tree-name" style={{ paddingLeft: row.depth * 16 }}>
                  <span className="tree-line">
                    <span
                      className="chevron"
                      aria-hidden="true"
                      onClick={(e) => {
                        e.stopPropagation()
                        toggle(row)
                      }}
                    >
                      {row.hasChildren ? (row.expanded ? '▾' : '▸') : ''}
                    </span>
                    <span className="kind">{label.kind}</span>
                    <span className="title">{label.title}</span>
                    {label.sub !== '' && <span className="sub dim">{label.sub}</span>}
                    {node.kind === 'call' && node.call.context_compacted && <span className="badge">compacted</span>}
                  </span>
                  {/* The counts take their own line and wrap, so neither they nor the title are cut off. */}
                  {(node.kind === 'prompt' || node.kind === 'agent') && (
                    <span className="counts dim">{countSummary(node)}</span>
                  )}
                </span>
                <Num text={formatDurationMs(node.duration)} />
                <Num
                  text={
                    node.kind === 'prompt' || node.kind === 'agent'
                      ? formatTimeSplit(node.llmMs, node.toolMs, node.toolEstimated)
                      : ''
                  }
                />
                <Num text={node.kind === 'tool' ? '' : formatCount(node.tokens)} />
                <Num text={node.kind === 'tool' ? '' : formatCostMicroUsd(node.cost)} />
                <span className="num">
                  {node.errors > 0 && (
                    <span className="badge badge-error">
                      {node.kind === 'tool' ? 'error' : formatCount(node.errors)}
                    </span>
                  )}
                </span>
                <span className="bar-cell">
                  {guides}
                  {Number.isFinite(from) && (
                    <span className={`bar bar-${node.kind}`} style={{ left: pct(from), width: pct(Math.max(0, to - from)) }} />
                  )}
                </span>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

const LANE_ROW = 1.5

/** Time mode: a waterfall by start time with one lane per agent. */
function SessionWaterfall({ model }: { model: SessionModel }) {
  const rem = useRem()
  const lanes = useMemo(() => buildLanes(model), [model])
  const scrollRef = useRef<HTMLDivElement>(null)
  // TanStack Virtual returns unmemoizable functions; the React Compiler skips this component.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: lanes.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => LANE_ROW * rem,
    overscan: 12,
  })
  // The row height is a count of rem: measure again when the text size changes.
  useEffect(() => {
    virtualizer.measure()
  }, [virtualizer, rem])
  const guides = useMemo(() => <AxisGuides axis={model.axis} compactions={model.compactions} />, [model])
  return (
    <div className="trace waterfall">
      <div className="lane-head" aria-hidden="true">
        <span>Agent</span>
        <AxisHeader axis={model.axis} compactions={model.compactions} />
      </div>
      <div ref={scrollRef} className="trace-scroll">
        <div role="list" aria-label="Session waterfall" style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((item) => (
            <Lane key={lanes[item.index].id} lane={lanes[item.index]} guides={guides} size={item.size} start={item.start} />
          ))}
        </div>
      </div>
    </div>
  )
}

function Lane({ lane, guides, size, start }: { lane: LaneRow; guides: React.ReactNode; size: number; start: number }) {
  return (
    <div
      role="listitem"
      aria-label={lane.first ? lane.label : `${lane.label}, more`}
      className={lane.first ? 'lane-row first' : 'lane-row'}
      style={{ height: size, transform: `translateY(${start}px)` }}
    >
      <span className="lane-label" style={{ paddingLeft: lane.depth * 12 }}>
        {lane.first ? (
          <>
            {lane.label} <span className="dim">{lane.agentId === '' ? '' : shortId(lane.agentId)}</span>
          </>
        ) : null}
      </span>
      <span className="bar-cell">
        {guides}
        {lane.items.map((it) => (
          <Link
            key={it.id}
            to="/calls/$id"
            params={{ id: it.callId }}
            search={{}}
            className={`bar bar-${it.kind}${it.error ? ' bar-error' : ''}`}
            style={{ left: pct(it.from), width: pct(Math.max(0, it.to - it.from)) }}
            aria-label={`${it.kind === 'call' ? 'LLM' : 'Tool'} ${it.label}, ${formatDurationMs(it.end - it.start)}${it.error ? ', error' : ''}`}
            title={`${it.label} · ${formatDurationMs(it.end - it.start)}`}
          />
        ))}
      </span>
    </div>
  )
}
