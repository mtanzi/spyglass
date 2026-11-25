import { Link } from '@tanstack/react-router'
import { formatCostMicroUsd, formatCount, formatText } from '../lib/format'
import type { SessionCostRow, ToolCostRow } from '../views/costOverviewData'
import { Tag } from './Badge'
import { InfoNote } from './InfoNote'
import { Maybe } from './Maybe'

/** A cost with "partial" and the unpriced count when some calls of the row have no price. */
function CostCell({ cost, unpriced }: { cost: number | null; unpriced: number }) {
  return (
    <>
      <Maybe text={formatCostMicroUsd(cost)} />
      {unpriced > 0 && cost !== null && <span className="dim">{` · partial: ${formatCount(unpriced)} unpriced`}</span>}
    </>
  )
}

/** The most costly sessions of the period, as the API ranks them. Each cost is the number the session list and the session view show. */
export function SessionCostTable({ sessions }: { sessions: SessionCostRow[] }) {
  return (
    <div className="table-frame">
      <table className="grid-table cost-table">
        <caption className="sr-only">Most costly sessions</caption>
        <thead>
          <tr>
            <th scope="col">Session</th>
            <th scope="col" className="num">Calls</th>
            <th scope="col" className="num">Cost</th>
            <th scope="col" className="num">Errors</th>
          </tr>
        </thead>
        <tbody>
          {sessions.map((s) => (
            <tr key={s.session_key}>
              <th scope="row" className="cell-clip">
                <Link to="/sessions/$key" params={{ key: s.session_key }} search={{}} className="mono">
                  {s.session_key}
                </Link>
              </th>
              <td className="num">{formatCount(s.calls)}</td>
              <td className="num">
                <CostCell cost={s.cost_micro_usd} unpriced={s.unpriced_calls} />
              </td>
              <td className="num">
                {s.errors > 0 ? formatCount(s.errors) : <span className="dim">none</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

const TOOL_COST_NOTE =
  "An estimate: a call's cost is split equally among its tool uses. This is not the cost the tool's result adds to later calls."

/** The most costly tools of the period. The cost is an estimate, and the column says so. */
export function ToolCostTable({ tools, window }: { tools: ToolCostRow[]; window: { from: string; to: string } }) {
  return (
    <div className="table-frame">
      <table className="grid-table cost-table">
        <caption className="sr-only">Most costly tools, by the estimated cost of the calls that requested them</caption>
        <thead>
          <tr>
            <th scope="col">Tool</th>
            <th scope="col" className="num">Uses</th>
            <th scope="col" className="num">Errors</th>
            <th scope="col" className="num">
              Estimated cost of the calls that requested it <InfoNote label="estimate" note={TOOL_COST_NOTE} />
            </th>
          </tr>
        </thead>
        <tbody>
          {tools.map((t) => (
            <tr key={t.tool}>
              <th scope="row" className="cell-clip">
                {t.tool === '' ? (
                  <Tag text={formatText(t.tool)} className="mono" />
                ) : (
                  <Link to="/tools" search={{ tool: [t.tool], ...window }} className="mono">
                    {t.tool}
                  </Link>
                )}
              </th>
              <td className="num">{formatCount(t.uses)}</td>
              <td className="num">
                {t.errors > 0 ? formatCount(t.errors) : <span className="dim">none</span>}
              </td>
              <td className="num">
                <CostCell cost={t.cost_micro_usd} unpriced={t.unpriced_calls} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
