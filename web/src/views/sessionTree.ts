// Session tree and waterfall model (ADR 0033). The agent tree is computed from
// the session's call rows when the page loads: session, prompt (user turn),
// agent (main or sub-agent), LLM call, tool call. A sub-agent nests under its
// parent agent from the parent-agent header. The tree is flattened into one
// list of rows for virtualisation, following agent-prism's model.

import type { ResponseOf } from '../api/client'
import { UNKNOWN, formatCount, zeroAsUnknown } from '../lib/format'
import { buildTimeAxis } from './timeAxis'
import type { Interval, TimeAxis } from './timeAxis'

export type SessionDetail = ResponseOf<'/admin/sessions/{key}'>
export type SessionCall = SessionDetail['calls'][number]
export type SessionAgent = SessionDetail['agents'][number]
export type ToolCall = ResponseOf<'/admin/tool-calls'>['tool_calls'][number]

/** Prompts expanded by default, counted from the latest. Older prompts start collapsed. */
export const LATEST_PROMPTS_EXPANDED = 3

interface NodeBase {
  id: string
  /** Real start and end, epoch ms. */
  start: number
  end: number
  /** Null when not known. */
  duration: number | null
  /** Tokens in plus out over the known calls; null when none is known. */
  tokens: number | null
  /** Micro-USD over the known costs; null when none is known. */
  cost: number | null
  /** Calls with an error and tool calls with an error result, in this subtree. */
  errors: number
  /** LLM calls in this subtree (a sub-agent's calls count for its parent agent too). */
  llmCalls: number
  /** Tool calls in this subtree, each counted once under the call that requested it. */
  toolCalls: number
  /** A call in this subtree resets the cache prefix. */
  compacted: boolean
  /** Index of the prompt this node belongs to, oldest first. */
  promptIndex: number
  children: TreeNode[]
}

export interface PromptNode extends NodeBase {
  kind: 'prompt'
  promptId: string
}
export interface AgentNode extends NodeBase {
  kind: 'agent'
  agentId: string
  parentAgentId: string
  agentType: string
}
export interface CallNode extends NodeBase {
  kind: 'call'
  call: SessionCall
}
export interface ToolNode extends NodeBase {
  kind: 'tool'
  tool: ToolCall
  /** The call this tool row opens: the use, or the result for a result without a use. */
  callId: string
}
export type TreeNode = PromptNode | AgentNode | CallNode | ToolNode

export interface AgentInfo {
  parentAgentId: string
  agentType: string
}

export interface SessionModel {
  prompts: PromptNode[]
  axis: TimeAxis
  /** Start times of the calls that reset the cache prefix, ascending. */
  compactions: number[]
  agents: Map<string, AgentInfo>
}

const ms = (iso: string | null | undefined) => (iso ? Date.parse(iso) : NaN)

export function callTokens(c: SessionCall): number | null {
  const u = c.usage
  return zeroAsUnknown(u.input_uncached + u.cache_read + u.cache_write_5m + u.cache_write_1h + u.output)
}

export function callHasError(c: SessionCall): boolean {
  return c.error_class !== '' || c.status >= 400
}

function callInterval(c: SessionCall): Interval {
  const start = ms(c.ts)
  return { start, end: start + Math.max(0, c.duration_ms) }
}

/**
 * Real interval of a tool call. The use is timed at the end of the response
 * that carried it, and the tool runs from there for its duration. A result
 * without a use is an instant at the result.
 */
export function toolInterval(t: ToolCall): Interval {
  const use = ms(t.use_ts)
  const result = ms(t.result_ts)
  if (!Number.isNaN(use)) {
    const end = t.duration_ms != null ? use + t.duration_ms : Number.isNaN(result) ? use : Math.max(use, result)
    return { start: use, end }
  }
  return { start: result, end: result }
}

/** Agent tuples by agent id. The first tuple with a parent wins. */
export function agentInfo(agents: SessionAgent[]): Map<string, AgentInfo> {
  const map = new Map<string, AgentInfo>()
  for (const a of agents) {
    const known = map.get(a.agent_id)
    if (!known || (known.parentAgentId === '' && a.parent_agent_id !== '')) {
      map.set(a.agent_id, { parentAgentId: a.parent_agent_id, agentType: a.agent_type })
    }
  }
  return map
}

function sumKnown(values: (number | null)[]): number | null {
  const known = values.filter((v): v is number => v !== null)
  return known.length === 0 ? null : known.reduce((a, b) => a + b, 0)
}

function base(id: string, promptIndex: number): NodeBase {
  return {
    id,
    start: Infinity,
    end: -Infinity,
    duration: null,
    tokens: null,
    cost: null,
    errors: 0,
    llmCalls: 0,
    toolCalls: 0,
    compacted: false,
    promptIndex,
    children: [],
  }
}

/** Tool calls per LLM call; null when there is no LLM call, so no division error shows. */
export function toolsPerCall(toolCalls: number, llmCalls: number): number | null {
  return llmCalls > 0 ? toolCalls / llmCalls : null
}

/** Ratio with one decimal; the unknown dash when null. */
export function formatToolsPerCall(ratio: number | null): string {
  return ratio == null ? UNKNOWN : ratio.toFixed(1)
}

/** Count summary of a prompt or agent row, e.g. "8 LLM calls · 23 tool calls · 2.9 tools per call". */
export function countSummary(node: Pick<NodeBase, 'llmCalls' | 'toolCalls'>): string {
  const plural = (n: number, one: string) => `${formatCount(n)} ${one}${n === 1 ? '' : 's'}`
  const parts = [plural(node.llmCalls, 'LLM call'), plural(node.toolCalls, 'tool call')]
  const ratio = toolsPerCall(node.toolCalls, node.llmCalls)
  if (ratio !== null) parts.push(`${formatToolsPerCall(ratio)} tools per call`)
  return parts.join(' · ')
}

const byStart = (a: TreeNode, b: TreeNode) => a.start - b.start || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)

function subtreeEnd(node: TreeNode): number {
  return node.kind === 'call' ? Math.max(node.end, ...node.children.map((c) => c.end).filter(Number.isFinite)) : node.end
}

/** Aggregates a prompt or agent node from its children, bottom-up, and sorts them by start. */
function finish(node: TreeNode): void {
  for (const child of node.children) finish(child)
  node.children.sort(byStart)
  if (node.kind === 'tool') {
    node.toolCalls = 1
    return
  }
  if (node.kind === 'call') {
    node.llmCalls = 1
    node.toolCalls = node.children.reduce((n, k) => n + k.toolCalls, 0)
    return
  }
  const kids = node.children
  if (kids.length === 0) {
    node.start = NaN
    node.end = NaN
    return
  }
  node.start = Math.min(...kids.map((k) => k.start).filter(Number.isFinite))
  // A tool call can run past the end of the call that requested it.
  node.end = Math.max(...kids.map(subtreeEnd).filter(Number.isFinite))
  node.duration = Number.isFinite(node.start) && Number.isFinite(node.end) ? node.end - node.start : null
  node.tokens = sumKnown(kids.map((k) => k.tokens))
  node.cost = sumKnown(kids.map((k) => k.cost))
  node.errors = kids.reduce((n, k) => n + k.errors, 0)
  node.llmCalls = kids.reduce((n, k) => n + k.llmCalls, 0)
  node.toolCalls = kids.reduce((n, k) => n + k.toolCalls, 0)
  node.compacted = kids.some((k) => k.compacted)
}

/**
 * Builds the tree: prompts oldest first; in each prompt the agents that made
 * its calls, a sub-agent under its parent agent; in each agent its calls and
 * sub-agents by start time; under each call the tool calls it requested (a
 * result without a use goes under the call that carried the result).
 */
export function buildSessionModel(detail: SessionDetail, tools: ToolCall[] = []): SessionModel {
  const agents = agentInfo(detail.agents)
  const calls = [...detail.calls].sort((a, b) => ms(a.ts) - ms(b.ts) || (a.id < b.id ? -1 : 1))

  // Prompts in order of their first call.
  const promptOrder: string[] = []
  const promptCalls = new Map<string, SessionCall[]>()
  for (const c of calls) {
    const key = c.prompt_id ?? ''
    let list = promptCalls.get(key)
    if (!list) {
      list = []
      promptCalls.set(key, list)
      promptOrder.push(key)
    }
    list.push(c)
  }

  const callNodes = new Map<string, CallNode>()
  const prompts: PromptNode[] = promptOrder.map((promptId, promptIndex) => {
    const prompt: PromptNode = { ...base(`p:${promptId}`, promptIndex), kind: 'prompt', promptId }
    const agentNodes = new Map<string, AgentNode>()
    const ensureAgent = (agentId: string, visiting: Set<string>): AgentNode => {
      const existing = agentNodes.get(agentId)
      if (existing) return existing
      const info = agents.get(agentId)
      const node: AgentNode = {
        ...base(`a:${promptId}:${agentId}`, promptIndex),
        kind: 'agent',
        agentId,
        parentAgentId: info?.parentAgentId ?? '',
        agentType: info?.agentType ?? '',
      }
      agentNodes.set(agentId, node)
      const parent = node.parentAgentId
      visiting.add(agentId)
      if (parent !== '' && !visiting.has(parent)) {
        ensureAgent(parent, visiting).children.push(node)
      } else {
        prompt.children.push(node)
      }
      return node
    }
    for (const c of promptCalls.get(promptId) ?? []) {
      const { start, end } = callInterval(c)
      const cost = c.cost_micro_usd
      const node: CallNode = {
        ...base(`c:${c.id}`, promptIndex),
        kind: 'call',
        call: c,
        start,
        end,
        duration: zeroAsUnknown(c.duration_ms),
        tokens: callTokens(c),
        cost,
        errors: callHasError(c) ? 1 : 0,
        compacted: c.context_compacted,
      }
      callNodes.set(c.id, node)
      ensureAgent(c.agent_id, new Set()).children.push(node)
    }
    return prompt
  })

  const seenTools = new Set<string>()
  for (const t of tools) {
    const id = `t:${t.tool_call_id}:${t.use_call_id ?? ''}:${t.result_call_id ?? ''}`
    if (seenTools.has(id)) continue
    seenTools.add(id)
    const owner = (t.use_call_id && callNodes.get(t.use_call_id)) || (t.result_call_id && callNodes.get(t.result_call_id))
    if (!owner) continue
    const { start, end } = toolInterval(t)
    const node: ToolNode = {
      ...base(id, owner.promptIndex),
      kind: 'tool',
      tool: t,
      callId: owner.call.id,
      start,
      end,
      duration: t.duration_ms,
      errors: t.is_error === true ? 1 : 0,
    }
    owner.children.push(node)
    // The call's own numbers stay its own; its errors count its tools too.
    owner.errors += node.errors
  }

  for (const p of prompts) finish(p)
  for (const c of callNodes.values()) c.children.sort(byStart)

  const intervals: Interval[] = []
  for (const c of callNodes.values()) {
    intervals.push({ start: c.start, end: c.end })
    for (const t of c.children) intervals.push({ start: t.start, end: t.end })
  }

  return {
    prompts,
    axis: buildTimeAxis(intervals),
    compactions: calls.filter((c) => c.context_compacted).map((c) => ms(c.ts)),
    agents,
  }
}

export interface FlatRow {
  node: TreeNode
  depth: number
  parentId: string | null
  expanded: boolean
  hasChildren: boolean
  /** Position among siblings, 1-based, and the sibling count (for aria-posinset and aria-setsize). */
  position: number
  siblings: number
}

/** Older prompts start collapsed; the latest LATEST_PROMPTS_EXPANDED start open. */
export function defaultExpanded(node: TreeNode, promptCount: number): boolean {
  return node.promptIndex >= promptCount - LATEST_PROMPTS_EXPANDED
}

/** Depth-first rows of the tree, entering only the expanded nodes. */
export function flattenTree(roots: TreeNode[], isExpanded: (node: TreeNode) => boolean): FlatRow[] {
  const rows: FlatRow[] = []
  const walk = (nodes: TreeNode[], depth: number, parentId: string | null) => {
    nodes.forEach((node, i) => {
      const hasChildren = node.children.length > 0
      const expanded = hasChildren && isExpanded(node)
      rows.push({ node, depth, parentId, expanded, hasChildren, position: i + 1, siblings: nodes.length })
      if (expanded) walk(node.children, depth + 1, node.id)
    })
  }
  walk(roots, 0, null)
  return rows
}

/** Short form of an id for dense rows. */
export function shortId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…` : id
}

export function agentLabel(agentId: string, info: AgentInfo | undefined): string {
  if (agentId === '') return 'unknown agent'
  const type = info?.agentType ?? ''
  if ((info?.parentAgentId ?? '') === '') return type !== '' ? `main · ${type}` : 'main agent'
  return type !== '' ? type : 'sub-agent'
}

/** Depth of an agent in the agent tree: 0 for a main agent. */
export function agentDepth(agentId: string, agents: Map<string, AgentInfo>): number {
  let depth = 0
  const seen = new Set([agentId])
  let parent = agents.get(agentId)?.parentAgentId ?? ''
  while (parent !== '' && !seen.has(parent)) {
    seen.add(parent)
    depth++
    parent = agents.get(parent)?.parentAgentId ?? ''
  }
  return depth
}

// Time mode: one lane per agent, sorted by first start. Overlapping items in a
// lane go to separate tracks, so parallel tool calls show as concurrent bars.

/** Narrowest bar on the axis, used for packing so that instants do not overlap. */
export const MIN_BAR_SHARE = 0.004

export interface LaneItem {
  id: string
  kind: 'call' | 'tool'
  label: string
  start: number
  end: number
  from: number
  to: number
  callId: string
  error: boolean
  compacted: boolean
}

export interface LaneRow {
  id: string
  agentId: string
  label: string
  depth: number
  /** First track of a lane carries its label. */
  first: boolean
  items: LaneItem[]
}

export function buildLanes(model: SessionModel): LaneRow[] {
  const byAgent = new Map<string, LaneItem[]>()
  const push = (agentId: string, item: LaneItem) => {
    const list = byAgent.get(agentId) ?? []
    list.push(item)
    byAgent.set(agentId, list)
  }
  const { position } = model.axis
  const visit = (node: TreeNode) => {
    if (node.kind === 'call') {
      const c = node.call
      push(c.agent_id, {
        id: node.id,
        kind: 'call',
        label: c.model || 'call',
        start: node.start,
        end: node.end,
        from: position(node.start),
        to: position(node.end),
        callId: c.id,
        error: callHasError(c),
        compacted: c.context_compacted,
      })
      for (const t of node.children) {
        if (t.kind !== 'tool') continue
        push(t.tool.agent_id || c.agent_id, {
          id: t.id,
          kind: 'tool',
          label: t.tool.name || 'tool',
          start: t.start,
          end: t.end,
          from: position(t.start),
          to: position(t.end),
          callId: t.callId,
          error: t.tool.is_error === true,
          compacted: false,
        })
      }
      return
    }
    node.children.forEach(visit)
  }
  model.prompts.forEach(visit)

  const lanes = [...byAgent.entries()]
    .map(([agentId, items]) => ({ agentId, items: items.sort((a, b) => a.start - b.start || (a.id < b.id ? -1 : 1)) }))
    .sort((a, b) => a.items[0].start - b.items[0].start || (a.agentId < b.agentId ? -1 : 1))

  const rows: LaneRow[] = []
  for (const lane of lanes) {
    const tracks: { end: number; items: LaneItem[] }[] = []
    for (const item of lane.items) {
      const right = Math.max(item.to, item.from + MIN_BAR_SHARE)
      const track = tracks.find((t) => t.end <= item.from)
      if (track) {
        track.items.push(item)
        track.end = right
      } else {
        tracks.push({ end: right, items: [item] })
      }
    }
    const label = agentLabel(lane.agentId, model.agents.get(lane.agentId))
    const depth = agentDepth(lane.agentId, model.agents)
    tracks.forEach((t, i) =>
      rows.push({ id: `lane:${lane.agentId}:${i}`, agentId: lane.agentId, label, depth, first: i === 0, items: t.items }),
    )
  }
  return rows
}
