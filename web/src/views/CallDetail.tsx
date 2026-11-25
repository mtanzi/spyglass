import { useQuery } from '@tanstack/react-query'
import { Link, useParams, useSearch } from '@tanstack/react-router'
import { detailQueryOptions } from '../api/queries'
import type { ResponseOf } from '../api/client'
import { StatusBadge, Tag } from '../components/Badge'
import { ContentNotStored } from '../components/ContentNotStored'
import { EmptyState, ErrorMessage, LoadingState } from '../components/states'
import {
  UNKNOWN,
  formatBytes,
  formatCostMicroUsd,
  formatCount,
  formatDurationMs,
  formatText,
  zeroAsUnknown,
} from '../lib/format'
import { CALL_TABS } from '../search/routes'
import type { CallTab } from '../search/routes'
import { Thread } from './callThread'
import { ToolOutcome, buildToolRows, useToolPairs } from './toolPairs'
import { cacheHitRatio, formatRatio } from './SessionList'

export type CallDetail = ResponseOf<'/admin/calls/{id}'>

const TAB_LABELS: Record<CallTab, string> = {
  overview: 'Overview',
  tools: 'Tools',
  usage: 'Usage and cost',
}

/**
 * V3 call detail (ADR 0032): stored numbers, names and hashes, never content.
 * Linear look as the session pages (ADR 0033): the tabs and a timing bar in
 * the main column, and a side panel with who made the call, the model, the
 * session link, tokens, cache and cost.
 */
export function CallDetailView() {
  const { id } = useParams({ from: '/calls/$id' })
  const { tab = 'overview' } = useSearch({ from: '/calls/$id' })
  // A finished call never changes, so the query is cached for the session.
  const query = useQuery(detailQueryOptions('/admin/calls/{id}', { path: { id } }))
  const call = query.data

  return (
    <section aria-labelledby="view-title" className="detail-host">
      <nav aria-label="Breadcrumb" className="crumbs">
        <Link to="/calls" search={{}}>
          Calls
        </Link>
        <span aria-hidden="true">/</span>
        <code>{id}</code>
      </nav>
      <div className="view-header">
        <h1 id="view-title" tabIndex={-1}>Call</h1>
        {call && (
          <>
            <Tag text={formatText(call.model)} className="mono" />
            <StatusBadge status={call.status} errorClass={call.error_class} />
            {call.usage_estimated && <Badge>Usage estimated</Badge>}
            {call.extraction_partial && <Badge>Extraction partial</Badge>}
          </>
        )}
      </div>
      {query.isPending && <LoadingState label="Loading call" />}
      {query.isError && <ErrorMessage error={query.error} onRetry={() => void query.refetch()} />}
      {call && (
        <div className="detail-grid">
          <div className="detail-main">
            <nav aria-label="Call detail tabs" className="tabs">
              {CALL_TABS.map((t) => (
                <Link
                  key={t}
                  to="/calls/$id"
                  params={{ id }}
                  search={t === 'overview' ? {} : { tab: t }}
                  aria-current={t === tab ? 'page' : undefined}
                >
                  {TAB_LABELS[t]}
                </Link>
              ))}
            </nav>
            {tab === 'overview' && <Overview call={call} />}
            {tab === 'tools' && <Tools call={call} />}
            {tab === 'usage' && <UsageAndCost call={call} />}
            <ContentNotStored />
          </div>
          <SidePanel call={call} />
        </div>
      )}
    </section>
  )
}

function Badge({ children }: { children: React.ReactNode }) {
  return <span className="badge">{children}</span>
}

function Value({ value }: { value: string | null | undefined }) {
  const shown = formatText(value)
  return shown === UNKNOWN ? <span className="unknown">{UNKNOWN}</span> : <>{shown}</>
}

function Fields({ label, rows }: { label: string; rows: [string, React.ReactNode][] }) {
  return (
    <section aria-label={label}>
      <h2>{label}</h2>
      <dl className="fields">
        {rows.map(([name, value]) => (
          <div key={name}>
            <dt>{name}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

function Unknown() {
  return <span className="unknown">{UNKNOWN}</span>
}

/** A formatted value; "unknown" is muted. */
function Text({ value }: { value: string }) {
  return value === UNKNOWN ? <Unknown /> : <>{value}</>
}

/** Who made the call, the call itself, and its tokens, cache and cost, beside every tab. */
function SidePanel({ call }: { call: CallDetail }) {
  const u = call.usage
  const tokensIn = zeroAsUnknown(u.input_uncached + u.cache_read + u.cache_write_5m + u.cache_write_1h)
  const cost = formatCostMicroUsd(call.cost_micro_usd)
  return (
    <aside className="side-panel" aria-label="Call summary">
      <Fields
        label="Model"
        rows={[
          ['Model', <Tag key="m" text={formatText(call.model)} className="mono" />],
          ['Provider', <Value key="pr" value={call.provider} />],
          ['Time', <Value key="t" value={call.ts} />],
          ['Upstream request id', <Value key="u" value={call.upstream_request_id} />],
        ]}
      />
      <Fields
        label="Who"
        rows={[
          ['Key', <Value key="k" value={call.key} />],
          ['Client', <Value key="c" value={call.client} />],
          ['Project', <Value key="pj" value={call.project} />],
          [
            'Session',
            call.session_key ? (
              <Link to="/sessions/$key" params={{ key: call.session_key }} className="mono">
                {call.session_key}
              </Link>
            ) : (
              <Unknown />
            ),
          ],
          ['Agent', <Value key="a" value={call.agent_id} />],
          ['Sub-agent of', <Value key="p" value={call.parent_agent_id} />],
          ['Prompt id', <Value key="pi" value={call.prompt_id} />],
        ]}
      />
      <Fields
        label="Tokens and cost"
        rows={[
          ['Tokens in', tokensIn === null ? <Unknown /> : formatCount(tokensIn)],
          ['Tokens out', zeroAsUnknown(u.output) === null ? <Unknown /> : formatCount(u.output)],
          ['Cache read', formatCount(u.cache_read)],
          ['Cache write', formatCount(u.cache_write_5m + u.cache_write_1h)],
          ['Cache hit', formatRatio(cacheHitRatio(u))],
          ['Cost', cost === UNKNOWN ? <Unknown /> : cost],
        ]}
      />
    </aside>
  )
}

const pct = (v: number) => `${(Math.min(1, Math.max(0, v)) * 100).toFixed(3)}%`

/**
 * Time to first byte, time to first token and total duration as bars from
 * the request start, on the total duration, in the session waterfall's style.
 * A time that was not recorded (0) reads as unknown and draws no bar.
 */
function Timing({ call }: { call: CallDetail }) {
  const total = zeroAsUnknown(call.duration_ms)
  const rows: [string, number | null, string][] = [
    ['Time to first byte', zeroAsUnknown(call.ttfb_ms), 'bar-ttfb'],
    ['Time to first token', zeroAsUnknown(call.ttft_ms), 'bar-ttft'],
    ['Duration', total, 'bar-total'],
  ]
  return (
    <section aria-label="Timing">
      <h2>Timing</h2>
      <dl className="timing">
        {rows.map(([label, value, bar]) => (
          <div key={label} className="timing-row">
            <dt>{label}</dt>
            <dd className="bar-cell" aria-hidden="true">
              {value !== null && total !== null && <span className={`bar ${bar}`} style={{ left: 0, width: pct(value / total) }} />}
            </dd>
            <dd className={value === null ? 'num unknown' : 'num'}>{formatDurationMs(value)}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

function Overview({ call }: { call: CallDetail }) {
  const { pairs } = useToolPairs(call)
  return (
    <>
      <Timing call={call} />
      {pairs.isError && <ErrorMessage error={pairs.error} onRetry={() => void pairs.refetch()} />}
      <Thread call={call} rows={buildToolRows(call, pairs.data?.tool_calls)} />
    </>
  )
}

function Tools({ call }: { call: CallDetail }) {
  const { pairs, enabled, hasTools } = useToolPairs(call)
  if (!hasTools) return <EmptyState>This call carries no tool uses or results.</EmptyState>
  const rows = buildToolRows(call, pairs.data?.tool_calls)
  return (
    <section aria-label="Tools">
      <h2>Tools</h2>
      {enabled && pairs.isPending && <LoadingState label="Loading tool pairs" />}
      {pairs.isError && <ErrorMessage error={pairs.error} onRetry={() => void pairs.refetch()} />}
      <div className="table-frame">
        <table className="grid-table">
          <thead>
            <tr>
              <th>Tool</th>
              <th>Call id</th>
              <th>In this call</th>
              <th className="num">Argument size</th>
              <th className="num">Result size</th>
              <th className="num">Duration</th>
              <th>Outcome</th>
              <th>Other call</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.key}>
                <td>
                  <Value value={row.name} />
                </td>
                <td>
                  <code>{row.toolCallId}</code>
                </td>
                <td className="dim">{row.role === 'requested' ? 'Requested' : 'Result'}</td>
                <td className="num">
                  <Text value={formatBytes(row.argBytes)} />
                </td>
                <td className="num">
                  <Text value={formatBytes(row.resultBytes)} />
                </td>
                <td className="num">
                  <Text value={formatDurationMs(row.duration)} />
                  {row.durationSource && ` (${row.durationSource})`}
                </td>
                <td>
                  <ToolOutcome row={row} />
                </td>
                <td>
                  {row.otherCallId ? (
                    <Link to="/calls/$id" params={{ id: row.otherCallId }}>
                      {row.role === 'requested' ? 'Resolving call' : 'Requesting call'}
                    </Link>
                  ) : (
                    <span className="unknown">{row.status === 'pending' ? 'none yet' : UNKNOWN}</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function UsageAndCost({ call }: { call: CallDetail }) {
  const u = call.usage
  const columns: [string, number][] = [
    ['Input (uncached)', u.input_uncached],
    ['Cache read', u.cache_read],
    ['Cache write 5m', u.cache_write_5m],
    ['Cache write 1h', u.cache_write_1h],
    ['Output', u.output],
    ['Reasoning (part of output)', u.reasoning],
  ]
  return (
    <section aria-label="Usage and cost">
      <h2>Usage and cost</h2>
      <div className="table-frame">
        <table className="grid-table">
          <thead>
            <tr>
              {columns.map(([name]) => (
                <th key={name} className="num">
                  {name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr>
              {columns.map(([name, value]) => (
                <td key={name} className="num">
                  {formatCount(value)}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      </div>
      <dl className="fields usage-fields">
        <div>
          <dt>Cost</dt>
          <dd>{formatCostMicroUsd(call.cost_micro_usd)}</dd>
        </div>
        <div>
          <dt>Pricing version</dt>
          <dd>
            <Value value={call.pricing_version} />
          </dd>
        </div>
      </dl>
      <details className="raw-usage">
        <summary>Raw usage</summary>
        {call.usage_raw == null ? (
          <p className="unknown">{UNKNOWN}</p>
        ) : (
          <pre>{JSON.stringify(call.usage_raw, null, 2)}</pre>
        )}
      </details>
    </section>
  )
}
