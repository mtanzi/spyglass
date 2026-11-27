import { formatCostMicroUsd, formatCount } from '../lib/format'
import { monthBars, monthLabel } from '../views/costOverviewData'
import type { MonthRow } from '../views/costOverviewData'
import { Maybe } from './Maybe'

/** Width of the drawing per month, and its bounds: a short period is drawn narrow, so its text stays readable when it is scaled to the panel. */
const PER_MONTH = 120
const MIN_W = 420
const MAX_W = 720
const H = 210
const TOP = 34
const BASE = 160
// The data end of a bar is rounded, its base is square on the baseline.
const CORNER = 4

/** A bar of width w and height h on the baseline at y0, rounded at the top only. */
function barPath(x: number, y0: number, w: number, h: number): string {
  const r = Math.min(CORNER, w / 2, h)
  return `M${x},${y0}V${y0 - h + r}Q${x},${y0 - h} ${x + r},${y0 - h}H${x + w - r}Q${x + w},${y0 - h} ${x + w},${y0 - h + r}V${y0}Z`
}

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
  const W = Math.min(MAX_W, Math.max(MIN_W, months.length * PER_MONTH))
  const step = W / Math.max(1, months.length)
  const width = Math.min(64, step * 0.6)
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
                <path
                  className={['month-bar', m.partial ? 'month-bar-partial' : '', isBest ? 'month-bar-best' : ''].filter(Boolean).join(' ')}
                  d={barPath(cx - width / 2, BASE, width, h)}
                />
              )}
              {isBest && (
                <text className="month-best" x={cx} y={y - 17} textAnchor="middle">
                  highest
                </text>
              )}
              <text className="month-value" x={cx} y={bar.height === null ? BASE - 6 : y - 4} textAnchor="middle">
                {m.calls === 0 ? 'no calls' : m.cost_micro_usd === null ? 'unknown' : formatCostMicroUsd(m.cost_micro_usd)}
              </text>
              <text className="month-axis" x={cx} y={BASE + 16} textAnchor="middle">
                {monthLabel(m.month)}
              </text>
              {notes.map((n, k) =>
                n ? (
                  <text key={n} className="month-note" x={cx} y={BASE + 29 + k * 12} textAnchor="middle">
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
