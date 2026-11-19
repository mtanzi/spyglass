import { expect, test } from 'vitest'
import { agentLabel, filterQuery, groupBySession, matchesAgentFilter, insertLive, LIVE_ROW_CAP, mergeRows, streamQuery } from './callLogRows'
import type { CallSummary } from './callLogRows'

export function call(id: string, over: Partial<CallSummary> = {}): CallSummary {
  return {
    id,
    ts: '2026-11-01T10:00:00Z',
    key: 'k',
    client: 'claude-code',
    session_key: '',
    agent_id: 'a',
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
    tool_count: 1,
    context_compacted: false,
    ...over,
  }
}

test('live insert puts the newest first and ignores a repeated id', () => {
  let rows: CallSummary[] = []
  rows = insertLive(rows, call('a'))
  rows = insertLive(rows, call('b'))
  expect(rows.map((r) => r.id)).toEqual(['b', 'a'])
  expect(insertLive(rows, call('a'))).toBe(rows)
})

test('live rows are capped at 500 and the oldest drop', () => {
  let rows: CallSummary[] = []
  for (let i = 0; i < LIVE_ROW_CAP + 25; i++) rows = insertLive(rows, call(`c${i}`))
  expect(rows).toHaveLength(LIVE_ROW_CAP)
  expect(rows[0].id).toBe(`c${LIVE_ROW_CAP + 24}`)
  expect(rows.at(-1)?.id).toBe('c25')
})

test('merge keeps one row per call id, live rows first', () => {
  const merged = mergeRows([call('b'), call('a')], [call('a'), call('c')])
  expect(merged.map((r) => r.id)).toEqual(['b', 'a', 'c'])
})

test('merge orders rows newest first, so a refetched newer call sorts above older live rows', () => {
  const live = [call('live-new', { ts: '2026-11-01T10:00:19Z' }), call('live-old', { ts: '2026-11-01T10:00:10Z' })]
  const paged = [call('missed', { ts: '2026-11-01T10:00:24Z' }), call('live-new', { ts: '2026-11-01T10:00:19Z' }), call('old', { ts: '2026-11-01T09:00:00.5Z' })]
  expect(mergeRows(live, paged).map((r) => r.id)).toEqual(['missed', 'live-new', 'live-old', 'old'])
})


test('group by session folds consecutive calls only', () => {
  const rows = groupBySession([
    call('1', { session_key: 's1' }),
    call('2', { session_key: 's1' }),
    call('3', { session_key: 's2' }),
    call('4', { session_key: '' }),
    call('5', { session_key: '' }),
    call('6', { session_key: 's1' }),
  ])
  expect(rows.map((r) => [r.kind, r.kind === 'group' ? r.calls.length : 1])).toEqual([
    ['group', 2],
    ['call', 1],
    ['call', 1],
    ['call', 1],
    ['call', 1],
  ])
})

test('the stream query leaves out a preset range', () => {
  expect(streamQuery({ range: '7d', model: ['m'] })).toMatchObject({ model: ['m'], from: undefined, to: undefined })
  expect(streamQuery({ from: '2026-01-01T00:00:00.000Z' }).from).toBe('2026-01-01T00:00:00.000Z')
})

test('agent label: main, sub-agent type, sub-agent id fallback, no session', () => {
  expect(agentLabel('s1', '')).toBe('main')
  expect(agentLabel('s1', 'abcdef1234567890', 'Explore')).toBe('Explore')
  expect(agentLabel('s1', 'abcdef1234567890')).toBe('abcdef12')
  expect(agentLabel('', '')).toBe('unknown')
})

test('agent filter main matches the label, other values match the stored id', () => {
  const main = call('m', { session_key: 's1', agent_id: '' })
  const sub = call('s', { session_key: 's1', agent_id: 'abc' })
  const bare = call('b', { session_key: '', agent_id: '' })
  expect([main, sub, bare].filter((c) => matchesAgentFilter('main', c))).toEqual([main])
  expect([main, sub, bare].filter((c) => matchesAgentFilter('abc', c))).toEqual([sub])
  expect(matchesAgentFilter(undefined, bare)).toBe(true)
  expect(filterQuery({ agent: ['main'] }).agent).toBeUndefined()
  expect(filterQuery({ agent: ['abc'] }).agent).toBe('abc')
})
