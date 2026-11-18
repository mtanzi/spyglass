import { expect, test } from 'vitest'
import { at, bigSession, detail, parallelSession, sessionCall, toolCall } from './sessionFixtures.test-util'
import {
  LATEST_PROMPTS_EXPANDED,
  buildLanes,
  buildSessionModel,
  countSummary,
  defaultExpanded,
  flattenTree,
  formatToolsPerCall,
  toolsPerCall,
} from './sessionTree'
import type { FlatRow, TreeNode } from './sessionTree'

const all = () => true
const describeRow = (r: FlatRow) => `${'  '.repeat(r.depth)}${r.node.id}`

test('flatten nests parallel sub-agents under their parent agent, in start order', () => {
  const { detail: d, tools } = parallelSession()
  const model = buildSessionModel(d, tools)
  const rows = flattenTree(model.prompts, all)
  expect(rows.map(describeRow)).toEqual([
    'p:p1',
    '  a:p1:main',
    '    c:m1',
    '    a:p1:subA',
    '      c:a1',
    '        t:ta:a1:a2',
    '      c:a2',
    '    a:p1:subB',
    '      c:b1',
    '        t:tb:b1:b2',
    '      c:b2',
    '    c:m2',
  ])
  const subA = rows.find((r) => r.node.id === 'a:p1:subA')!
  const subB = rows.find((r) => r.node.id === 'a:p1:subB')!
  // Both sub-agents are siblings under main and overlap in time.
  expect(subA.parentId).toBe('a:p1:main')
  expect(subB.parentId).toBe('a:p1:main')
  expect(subA.node.start).toBeLessThan(subB.node.end)
  expect(subB.node.start).toBeLessThan(subA.node.end)
  expect([subA.position, subA.siblings]).toEqual([2, 4])
})

test('agent and prompt rows aggregate duration, tokens, cost and errors', () => {
  const { detail: d, tools } = parallelSession()
  const model = buildSessionModel(d, tools)
  const [prompt] = model.prompts
  // m1 starts at 0 and m2 ends at 900.
  expect(prompt.duration).toBe(900)
  // Each call has 10 tokens and costs 10 micro-USD.
  expect(prompt.tokens).toBe(60)
  expect(prompt.cost).toBe(60)
  // a2 failed and tool tb returned an error.
  expect(prompt.errors).toBe(2)
  const subA = prompt.children[0].children.find((n) => n.id === 'a:p1:subA')!
  expect(subA.errors).toBe(1)
  expect(subA.duration).toBe(500)
})

test('unknown cost stays unknown and is not summed as zero', () => {
  const d = detail([
    sessionCall('x', { cost_micro_usd: null }),
    sessionCall('y', { offset: 10, cost_micro_usd: null }),
  ])
  const model = buildSessionModel(d)
  expect(model.prompts[0].cost).toBeNull()
  const mixed = buildSessionModel(detail([sessionCall('x', { cost_micro_usd: null }), sessionCall('y', { offset: 10 })]))
  expect(mixed.prompts[0].cost).toBe(10)
})

test('a sub-agent whose parent made no call in the prompt still nests under that parent', () => {
  const d = detail(
    [sessionCall('s', { agent_id: 'sub', prompt_id: 'p2' })],
    [
      { agent_id: 'main', parent_agent_id: '', agent_type: '', calls: 0 },
      { agent_id: 'sub', parent_agent_id: 'main', agent_type: 'Explore', calls: 1 },
    ],
  )
  const rows = flattenTree(buildSessionModel(d).prompts, all)
  expect(rows.map(describeRow)).toEqual(['p:p2', '  a:p2:main', '    a:p2:sub', '      c:s'])
})

test('a parent-agent cycle does not loop', () => {
  const d = detail(
    [sessionCall('x', { agent_id: 'a' }), sessionCall('y', { agent_id: 'b', offset: 5 })],
    [
      { agent_id: 'a', parent_agent_id: 'b', agent_type: '', calls: 1 },
      { agent_id: 'b', parent_agent_id: 'a', agent_type: '', calls: 1 },
    ],
  )
  const rows = flattenTree(buildSessionModel(d).prompts, all)
  expect(rows.filter((r) => r.node.kind === 'call')).toHaveLength(2)
})

test('calls without a prompt id form their own group; prompts are oldest first', () => {
  const d = detail([
    sessionCall('late', { offset: 100, prompt_id: 'p2' }),
    sessionCall('none', { offset: 50, prompt_id: '' }),
    sessionCall('early', { offset: 0, prompt_id: 'p1' }),
  ])
  const model = buildSessionModel(d)
  expect(model.prompts.map((p) => p.promptId)).toEqual(['p1', '', 'p2'])
})

test('a result without a use nests under the call that carried the result', () => {
  const d = detail([sessionCall('r', { offset: 0 })])
  const model = buildSessionModel(d, [
    toolCall('orphan-use', { status: 'result_only', name: '', result_call_id: 'r', result_ts: at(0), duration_ms: null }),
    toolCall('elsewhere', { use_call_id: 'not-in-session', use_ts: at(0) }),
  ])
  const rows = flattenTree(model.prompts, all)
  expect(rows.map((r) => r.node.id)).toContain('t:orphan-use::r')
  expect(rows.some((r) => r.node.id.startsWith('t:elsewhere'))).toBe(false)
})

test('older prompts start collapsed, the latest ones open', () => {
  const calls = Array.from({ length: 6 }, (_, i) => sessionCall(`c${i}`, { offset: i * 10, prompt_id: `p${i}` }))
  const model = buildSessionModel(detail(calls))
  const rows = flattenTree(model.prompts, (n: TreeNode) => defaultExpanded(n, model.prompts.length))
  const prompts = rows.filter((r) => r.node.kind === 'prompt')
  expect(prompts).toHaveLength(6)
  expect(prompts.map((r) => r.expanded)).toEqual([
    ...Array(6 - LATEST_PROMPTS_EXPANDED).fill(false),
    ...Array(LATEST_PROMPTS_EXPANDED).fill(true),
  ])
  // Collapsed prompts hide their calls.
  expect(rows.filter((r) => r.node.kind === 'call').map((r) => r.node.id)).toEqual(['c:c3', 'c:c4', 'c:c5'])
})

test('compaction points are the starts of the compacted calls', () => {
  const d = detail([sessionCall('a'), sessionCall('b', { offset: 300, context_compacted: true })])
  expect(buildSessionModel(d).compactions).toEqual([Date.parse(at(300))])
})

test('time mode has one lane per agent; parallel tool calls in one agent get separate tracks', () => {
  const { detail: d, tools } = parallelSession()
  const lanes = buildLanes(buildSessionModel(d, tools))
  expect(lanes.filter((l) => l.first).map((l) => l.agentId)).toEqual(['main', 'subA', 'subB'])

  const one = detail([sessionCall('c', { duration_ms: 100 })])
  const parallel = [
    toolCall('t1', { use_call_id: 'c', use_ts: at(100), duration_ms: 500 }),
    toolCall('t2', { use_call_id: 'c', use_ts: at(100), duration_ms: 400 }),
  ]
  const rows = buildLanes(buildSessionModel(one, parallel))
  expect(rows.every((r) => r.agentId === 'main')).toBe(true)
  // The call and the first tool share a track; the second, concurrent tool gets its own.
  expect(rows.map((r) => r.items.map((i) => i.id))).toEqual([['c:c', 't:t1:c:'], ['t:t2:c:']])
})

test('1,000-call session: building and flattening stays fast', () => {
  const { detail: d, tools } = bigSession(1000)
  const startedAt = performance.now()
  const model = buildSessionModel(d, tools)
  const rows = flattenTree(model.prompts, all)
  const lanes = buildLanes(model)
  const elapsed = performance.now() - startedAt
  // 20 prompts, 2 agents each, 1,000 calls and 2,000 tool calls.
  expect(rows.length).toBe(20 + 40 + 1000 + 2000)
  expect(lanes.filter((l) => l.first)).toHaveLength(2)
  expect(model.axis.gaps.length).toBeGreaterThan(0)
  expect(model.compactions).toHaveLength(1)
  expect(elapsed).toBeLessThan(1000)
})

test('an agent span covers a tool call that runs past its last call', () => {
  const d = detail([sessionCall('c', { duration_ms: 100 })])
  const model = buildSessionModel(d, [toolCall('t', { use_call_id: 'c', use_ts: at(100), duration_ms: 900 })])
  const [agent] = model.prompts[0].children
  expect(agent.duration).toBe(1000)
  expect(model.prompts[0].duration).toBe(1000)
})

test('prompt and agent rows count LLM calls and tool calls, a sub-agent counting for its parent', () => {
  const { detail: d, tools } = parallelSession()
  const [prompt] = buildSessionModel(d, tools).prompts
  expect([prompt.llmCalls, prompt.toolCalls]).toEqual([6, 2])
  expect(countSummary(prompt)).toBe('6 LLM calls · 2 tool calls · 0.3 tools per call')
  const main = prompt.children[0]
  // The main agent holds its own two calls plus both sub-agents.
  expect([main.llmCalls, main.toolCalls]).toEqual([6, 2])
  const subA = main.children.find((n) => n.id === 'a:p1:subA')!
  expect([subA.llmCalls, subA.toolCalls]).toEqual([2, 1])
  expect(countSummary(subA)).toBe('2 LLM calls · 1 tool call · 0.5 tools per call')
})

test('tool calls are counted once, under the call that requested them', () => {
  const d = detail([sessionCall('x'), sessionCall('y', { offset: 10 })])
  const tools = [
    toolCall('t1', { use_call_id: 'x', result_call_id: 'y', use_ts: at(5) }),
    toolCall('t2', { use_call_id: 'x', use_ts: at(6) }),
    toolCall('t3', { result_call_id: 'y', result_ts: at(20) }),
  ]
  const [prompt] = buildSessionModel(d, [...tools, tools[0]]).prompts
  expect([prompt.llmCalls, prompt.toolCalls]).toEqual([2, 3])
  expect(countSummary(prompt)).toBe('2 LLM calls · 3 tool calls · 1.5 tools per call')
})

test('a scope with no LLM call shows no ratio and no division error', () => {
  expect(toolsPerCall(3, 0)).toBeNull()
  expect(formatToolsPerCall(null)).toBe('unknown')
  expect(countSummary({ llmCalls: 0, toolCalls: 0 })).toBe('0 LLM calls · 0 tool calls')
  expect(toolsPerCall(0, 4)).toBe(0)
  expect(formatToolsPerCall(23 / 8)).toBe('2.9')
  expect(countSummary({ llmCalls: 1, toolCalls: 1 })).toBe('1 LLM call · 1 tool call · 1.0 tools per call')
})

function findNode(nodes: TreeNode[], id: string): TreeNode | undefined {
  for (const n of nodes) {
    const found = n.id === id ? n : findNode(n.children, id)
    if (found) return found
  }
  return undefined
}
const nodeById = (nodes: TreeNode[], id: string): TreeNode => {
  const n = findNode(nodes, id)
  if (!n) throw new Error(`no node ${id}`)
  return n
}

test('time split sums sequential call and tool durations per prompt and agent', () => {
  const calls = [
    sessionCall('c1', { offset: 0, duration_ms: 1000 }),
    sessionCall('c2', { offset: 3000, duration_ms: 2000 }),
  ]
  const tools = [
    toolCall('t1', { use_call_id: 'c1', result_call_id: 'c2', use_ts: at(1000), duration_ms: 500 }),
    toolCall('t2', { use_call_id: 'c2', use_ts: at(5000), duration_ms: 1500 }),
  ]
  const model = buildSessionModel(detail(calls), tools)
  const prompt = model.prompts[0]
  const agent = nodeById(model.prompts, 'a:p1:main')
  for (const n of [prompt, agent]) {
    expect([n.llmMs, n.toolMs, n.toolEstimated]).toEqual([3000, 2000, false])
  }
})

test('time split adds parallel tool calls and parallel sub-agents as busy time', () => {
  const { detail: d, tools } = parallelSession()
  const model = buildSessionModel(d, tools)
  // Tool calls ta (80 ms) and tb (90 ms) overlap in wall time and both count.
  const subA = nodeById(model.prompts, 'a:p1:subA')
  const subB = nodeById(model.prompts, 'a:p1:subB')
  expect([subA.llmMs, subA.toolMs]).toEqual([400, 80])
  expect([subB.llmMs, subB.toolMs]).toEqual([400, 90])
  const main = nodeById(model.prompts, 'a:p1:main')
  expect([main.llmMs, main.toolMs]).toEqual([1000, 170])
  expect(model.prompts[0].llmMs).toBe(1000)
  expect(model.prompts[0].toolMs).toBe(170)
})

test('time split of a scope with no tool calls has zero tool time', () => {
  const model = buildSessionModel(detail([sessionCall('c1', { duration_ms: 700 })]), [])
  const prompt = model.prompts[0]
  expect([prompt.llmMs, prompt.toolMs, prompt.toolEstimated]).toEqual([700, 0, false])
})

test('time split marks the scope estimated when a tool duration is estimated, and skips unknown durations', () => {
  const calls = [sessionCall('c1', { duration_ms: 100 }), sessionCall('c2', { offset: 500, duration_ms: 100, agent_id: 'other' })]
  const tools = [
    toolCall('t1', { use_call_id: 'c1', use_ts: at(100), duration_ms: 300, duration_source: 'estimated' }),
    toolCall('t2', { use_call_id: 'c1', use_ts: at(100), duration_ms: null, duration_source: null, status: 'pending' }),
    toolCall('t3', { use_call_id: 'c2', use_ts: at(600), duration_ms: 50, duration_source: 'header' }),
  ]
  const model = buildSessionModel(detail(calls, [
    { agent_id: 'main', parent_agent_id: '', agent_type: '', calls: 1 },
    { agent_id: 'other', parent_agent_id: '', agent_type: '', calls: 1 },
  ]), tools)
  const main = nodeById(model.prompts, 'a:p1:main')
  const other = nodeById(model.prompts, 'a:p1:other')
  expect([main.toolMs, main.toolEstimated]).toEqual([300, true])
  expect([other.toolMs, other.toolEstimated]).toEqual([50, false])
  expect([model.prompts[0].toolMs, model.prompts[0].toolEstimated]).toEqual([350, true])
})
