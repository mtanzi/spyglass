import { formatCostMicroUsd, formatCount } from '../lib/format'
import { monthBars, monthLabel } from '../views/costOverviewData'
import type { MonthRow } from '../views/costOverviewData'
import { Maybe } from './Maybe'

const W = 600
const H = 200
const TOP = 32
const BASE = 150

/** What a reader gets from the chart in words, for the label of the figure. */
export function monthChartLabel(months: MonthRow[], best: MonthRow | undefined, total: number | null): string {
  const span = `${months.length} ${months.length === 1 ? 'month' : 'months'}`
  const most =
    best === undefined ? '' : `; most costly ${monthLabel(best.month, 'long')}, ${formatCostMicroUsd(best.cost_micro_usd)}`
  return `Cost per month: ${formatCostMicroUsd(total)} in ${span}${most}`
}

function barTitle(m: MonthRow): string {
  const parts = [monthLabel(m.month, 'long')]
  if (m.calls === 0) return `${parts[0]}: no calls`
  const cost = m.cost_micro_usd === null ? 'cost unknown' : formatCostMicroUsd(m.cost_micro_usd)
  parts.push(`${cost}${m.partial ? ' so far' : ''}`, `${formatCount(m.calls)} calls`)
  if (m.unpriced_calls > 0) parts.push(`partial: ${formatCount(m.unpriced_calls)} unpriced`)
  return parts.join(', ')
}

/**
 * Cost per calendar month as bars of one series, with a value label over each
 * bar. The most costly month has a stronger outline and the word "highest".
 * The running month has a dashed outline and "so far". A month with unpriced
 * calls says "partial" and a month with no known cost draws no bar. Plain
 * SVG, as the activity chart. The table below it holds every value.
 */
export function MonthlyCostChart({
  months,
  best,
  total,
}: {
  months: MonthRow[]
  best: MonthRow | undefined
  total: number | null
}) {
  const bars = monthBars(months)
  const step = W / Math.max(1, months.length)
  const width = Math.min(48, step * 0.7)
  return (
    <figure className="month-chart">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={monthChartLabel(months, best, total)}>
        <line className="month-baseline" x1="0" x2={W} y1={BASE} y2={BASE} />
        {months.map((m, i) => {
          const bar = bars[i]
          const cx = i * step + step / 2
          const h = bar.height === null ? 0 : Math.max(bar.height * (BASE - TOP), bar.height > 0 ? 2 : 0)
          const y = BASE - h
          const isBest = best !== undefined && best.month === m.month
          const notes = [m.partial && m.calls > 0 ? 'so far' : '', m.unpriced_calls > 0 && m.cost_micro_usd !== null ? 'partial' : '']
          return (
            <g key={m.month} className="month-group">
              <title>{barTitle(m)}</title>
              {bar.height !== null && (
                <rect
                  className={['month-bar', m.partial ? 'month-bar-partial' : '', isBest ? 'month-bar-best' : ''].filter(Boolean).join(' ')}
                  x={cx - width / 2}
                  y={y}
                  width={width}
                  height={h}
                />
              )}
              {isBest && (
                <text className="month-best" x={cx} y={y - 14} textAnchor="middle">
                  highest
                </text>
              )}
              <text className="month-value" x={cx} y={bar.height === null ? BASE - 6 : y - 4} textAnchor="middle">
                {m.calls === 0 ? 'no calls' : m.cost_micro_usd === null ? 'unknown' : formatCostMicroUsd(m.cost_micro_usd)}
              </text>
              <text className="month-axis" x={cx} y={BASE + 14} textAnchor="middle">
                {monthLabel(m.month)}
              </text>
              {notes.map((n, k) =>
                n ? (
                  <text key={n} className="month-note" x={cx} y={BASE + 26 + k * 11} textAnchor="middle">
                    {n}
                  </text>
                ) : null,
              )}
            </g>
          )
        })}
      </svg>
      <details className="chart-table">
        <summary>Show as table</summary>
        <div className="table-frame chart-table-frame">
          <table className="grid-table">
            <caption className="sr-only">Cost per month</caption>
            <thead>
              <tr>
                <th scope="col">Month</th>
                <th scope="col" className="num">Calls</th>
                <th scope="col" className="num">Cost</th>
                <th scope="col" className="num">Unpriced calls</th>
              </tr>
            </thead>
            <tbody>
              {months.map((m) => (
                <tr key={m.month}>
                  <th scope="row">
                    {monthLabel(m.month, 'long')}
                    {m.partial && m.calls > 0 && <span className="dim"> so far</span>}
                  </th>
                  <td className="num">{formatCount(m.calls)}</td>
                  <td className="num">
                    {m.calls === 0 ? <span className="dim">no calls</span> : <Maybe text={formatCostMicroUsd(m.cost_micro_usd)} />}
                  </td>
                  <td className="num">{formatCount(m.unpriced_calls)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  )
}
