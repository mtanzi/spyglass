// Fixtures for the session tree, list and view tests.
import type { CallSummary } from './callLogRows'
import type { SessionDetail, ToolCall } from './sessionTree'

export const T0 = Date.parse('2026-11-01T10:00:00Z')
export const at = (ms: number) => new Date(T0 + ms).toISOString()

function call(id: string, over: Partial<CallSummary>): CallSummary {
  return {
    id,
    ts: at(0),
    key: 'ci',
    client: 'claude-code',
    session_key: '',
    agent_id: 'a',
    project: '',
    provider: 'anthropic',
    model: 'm',
    status: 200,
    error_class: '',
    stop_reason: 'end_turn',
    streamed: true,
    ttft_ms: 10,
    duration_ms: 100,
    usage: { input_uncached: 1, cache_read: 2, cache_write_5m: 3, cache_write_1h: 0, output: 4, reasoning: 0 },
    cost_micro_usd: 10,
    tool_count: 0,
    context_compacted: false,
    ...over,
  }
}

export function sessionCall(id: string, over: Partial<CallSummary> & { offset?: number } = {}): CallSummary {
  const { offset = 0, ...rest } = over
  return call(id, { session_key: 's1', ts: at(offset), prompt_id: 'p1', agent_id: 'main', tool_count: 0, ...rest })
}

export function toolCall(id: string, over: Partial<ToolCall> = {}): ToolCall {
  return {
    tool_call_id: id,
    name: 'Bash',
    source: 'client',
    status: 'paired',
    session_key: 's1',
    agent_id: 'main',
    use_call_id: null,
    result_call_id: null,
    use_ts: null,
    result_ts: null,
    duration_ms: 50,
    duration_source: 'header',
    is_error: false,
    arg_bytes: 10,
    result_bytes: 20,
    ...over,
  }
}

export type AgentTuple = SessionDetail['agents'][number]

export function detail(calls: CallSummary[], agents: AgentTuple[] = [], over: Partial<SessionDetail['summary']> = {}): SessionDetail {
  const sorted = [...calls].sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts))
  return {
    summary: {
      session_key: 's1',
      key: 'ci',
      client: 'claude-code',
      first_ts: sorted[0]?.ts ?? at(0),
      last_ts: sorted[sorted.length - 1]?.ts ?? at(0),
      ended_at: sorted[sorted.length - 1]?.ts ?? at(0),
      calls: calls.length,
      prompts: new Set(calls.map((c) => c.prompt_id).filter((p) => p !== '')).size,
      tool_calls: 0,
      errors: calls.filter((c) => c.error_class !== '').length,
      agent_count: new Set(calls.map((c) => c.agent_id)).size,
      models: ['m'],
      usage: { input_uncached: 10, cache_read: 30, cache_write_5m: 0, cache_write_1h: 0, output: 5, reasoning: 0 },
      cost_micro_usd: 1_000,
      unpriced_calls: 0,
      llm_ms: 0,
      tool_ms: 0,
      tool_ms_estimated: false,
      ...over,
    },
    agents: agents.length > 0 ? agents : [{ agent_id: 'main', parent_agent_id: '', agent_type: '', calls: calls.length }],
    prompts: [],
    // The API returns calls newest first.
    calls: sorted.reverse(),
  }
}

/**
 * One prompt where the main agent spawns two sub-agents that run in
 * parallel, each with its own calls and a tool call.
 */
export function parallelSession(): { detail: SessionDetail; tools: ToolCall[] } {
  const calls = [
    sessionCall('m1', { offset: 0, duration_ms: 100 }),
    sessionCall('a1', { offset: 200, duration_ms: 300, agent_id: 'subA' }),
    sessionCall('b1', { offset: 250, duration_ms: 300, agent_id: 'subB' }),
    sessionCall('a2', { offset: 600, duration_ms: 100, agent_id: 'subA', error_class: 'overloaded', status: 529 }),
    sessionCall('b2', { offset: 650, duration_ms: 100, agent_id: 'subB' }),
    sessionCall('m2', { offset: 800, duration_ms: 100 }),
  ]
  const agents: AgentTuple[] = [
    { agent_id: 'main', parent_agent_id: '', agent_type: '', calls: 2 },
    { agent_id: 'subA', parent_agent_id: 'main', agent_type: 'Explore', calls: 2 },
    { agent_id: 'subB', parent_agent_id: 'main', agent_type: 'Plan', calls: 2 },
  ]
  const tools = [
    toolCall('ta', { agent_id: 'subA', use_call_id: 'a1', result_call_id: 'a2', use_ts: at(500), result_ts: at(600), duration_ms: 80 }),
    toolCall('tb', { agent_id: 'subB', use_call_id: 'b1', result_call_id: 'b2', use_ts: at(550), result_ts: at(650), duration_ms: 90, is_error: true }),
  ]
  return { detail: detail(calls, agents), tools }
}

/** A session of `n` calls spread over `prompts` prompts and two agents, each call with two tool calls. */
export function bigSession(n: number, prompts = 20): { detail: SessionDetail; tools: ToolCall[] } {
  const calls: CallSummary[] = []
  const tools: ToolCall[] = []
  for (let i = 0; i < n; i++) {
    const id = `c${String(i).padStart(5, '0')}`
    const offset = i * 2000 + (i % 50 === 0 ? i * 60_000 : 0)
    calls.push(
      sessionCall(id, {
        offset,
        duration_ms: 1500,
        prompt_id: `p${Math.floor((i * prompts) / n)}`,
        agent_id: i % 3 === 0 ? 'sub' : 'main',
        context_compacted: i === Math.floor(n / 2),
      }),
    )
    for (let k = 0; k < 2; k++) {
      tools.push(toolCall(`${id}-t${k}`, { use_call_id: id, use_ts: at(offset + 1500), duration_ms: 200 + k * 100 }))
    }
  }
  const agents: AgentTuple[] = [
    { agent_id: 'main', parent_agent_id: '', agent_type: '', calls: n },
    { agent_id: 'sub', parent_agent_id: 'main', agent_type: 'Explore', calls: n },
  ]
  return { detail: detail(calls, agents), tools }
}
