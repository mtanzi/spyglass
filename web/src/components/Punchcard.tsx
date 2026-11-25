import { formatCostMicroUsd, formatCount } from '../lib/format'
import {
  cellName,
  cellsByCost,
  hourRange,
  knownTotal,
  markerRadius,
  mostCostlyCell,
  punchcardLabel,
  weekdayName,
  WEEKDAYS,
} from '../views/costOverviewData'
import type { PunchcardCell } from '../views/costOverviewData'
import { Maybe } from './Maybe'

const STEP = 22
const LEFT = 40
const TOP = 4
const HOURS = 24
const W = LEFT + HOURS * STEP
const H = TOP + WEEKDAYS.length * STEP + 18
const MAX_R = 10
// A cell with a known cost of almost nothing still draws a dot.
const MIN_R = 1.5
const UNKNOWN_R = 3.5

function cellTitle(c: PunchcardCell): string {
  const cost = c.cost_micro_usd === null ? 'cost unknown' : formatCostMicroUsd(c.cost_micro_usd)
  const parts = [cellName(c), cost, `${formatCount(c.calls)} ${c.calls === 1 ? 'call' : 'calls'}`]
  if (c.unpriced_calls > 0 && c.cost_micro_usd !== null) parts.push(`partial: ${formatCount(c.unpriced_calls)} unpriced`)
  return parts.join(', ')
}

/**
 * Cost by weekday (rows, Monday first) and hour of day (columns), on the wall
 * clock of the zone the cells were cut in. Each cell with a known cost is a
 * circle whose area is proportional to the cost; size carries the value and
 * the colour is only brass. A cell with calls and no known cost is a small
 * hollow circle. The most costly cell has a stronger outline and is named in
 * the caption. Plain SVG; the table below it lists every cell.
 */
export function Punchcard({ cells, tz }: { cells: PunchcardCell[]; tz: string }) {
  const best = mostCostlyCell(cells)
  const bestCell = best.kind === 'known' ? best.row : undefined
  const max = Math.max(0, ...cells.map((c) => c.cost_micro_usd ?? 0))
  const total = knownTotal(cells)
  return (
    <figure className="punchcard">
      <div className="punchcard-scroll" tabIndex={0} role="region" aria-label="Punchcard, scrolls sideways on a narrow screen">
        <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={punchcardLabel(cells, bestCell)}>
          {WEEKDAYS.map((day, i) => (
            <text key={day} className="punch-axis" x={LEFT - 6} y={TOP + i * STEP + STEP / 2} textAnchor="end" dominantBaseline="central">
              {day.slice(0, 3)}
            </text>
          ))}
          {Array.from({ length: HOURS }, (_, h) =>
            h % 3 === 0 ? (
              <text key={h} className="punch-axis" x={LEFT + h * STEP + STEP / 2} y={TOP + WEEKDAYS.length * STEP + 12} textAnchor="middle">
                {String(h).padStart(2, '0')}
              </text>
            ) : null,
          )}
          {WEEKDAYS.map((day, i) => (
            <line key={day} className="punch-row" x1={LEFT} x2={W} y1={TOP + i * STEP + STEP / 2} y2={TOP + i * STEP + STEP / 2} />
          ))}
          {cells.map((c) => {
            const cx = LEFT + c.hour * STEP + STEP / 2
            const cy = TOP + (c.weekday - 1) * STEP + STEP / 2
            const radius = markerRadius(c.cost_micro_usd, max, MAX_R)
            const isBest = bestCell !== undefined && bestCell.weekday === c.weekday && bestCell.hour === c.hour
            return (
              <g key={`${c.weekday}-${c.hour}`} className="punch-cell">
                <title>{cellTitle(c)}</title>
                {radius === null ? (
                  <circle className="punch-dot punch-dot-unknown" cx={cx} cy={cy} r={UNKNOWN_R} />
                ) : (
                  <circle
                    className={isBest ? 'punch-dot punch-dot-best' : 'punch-dot'}
                    cx={cx}
                    cy={cy}
                    r={Math.max(radius, MIN_R)}
                  />
                )}
              </g>
            )
          })}
        </svg>
      </div>
      <figcaption className="punch-caption">
        {bestCell !== undefined ? (
          <>
            Most costly hour: {cellName(bestCell)}, <Maybe text={formatCostMicroUsd(bestCell.cost_micro_usd)} />
            {best.kind === 'known' && best.partial && <span className="dim"> · partial</span>}
          </>
        ) : (
          <>Cost is unknown: the markers show the hours that have calls.</>
        )}
        <span className="dim">{` · Larger circle, higher cost · ${tz}`}</span>
      </figcaption>
      <details className="chart-table">
        <summary>Show as table</summary>
        <div className="table-frame chart-table-frame">
          <table className="grid-table">
            <caption className="sr-only">{`Cost by weekday and hour, highest first${total === null ? '' : `, ${formatCostMicroUsd(total)} in all`}`}</caption>
            <thead>
              <tr>
                <th scope="col">Weekday</th>
                <th scope="col">Hour</th>
                <th scope="col" className="num">Calls</th>
                <th scope="col" className="num">Cost</th>
                <th scope="col" className="num">Unpriced calls</th>
              </tr>
            </thead>
            <tbody>
              {cellsByCost(cells).map((c) => (
                <tr key={`${c.weekday}-${c.hour}`}>
                  <th scope="row">{weekdayName(c.weekday)}</th>
                  <td>{hourRange(c.hour)}</td>
                  <td className="num">{formatCount(c.calls)}</td>
                  <td className="num">
                    <Maybe text={formatCostMicroUsd(c.cost_micro_usd)} />
                  </td>
                  <td className="num">{formatCount(c.unpriced_calls)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  )
}
