import type { ReactNode } from 'react'
import { formatCostMicroUsd, formatCount, formatText } from '../lib/format'
import type { GroupTotal } from '../views/dashboardData'
import { Tag } from './Badge'
import { Maybe } from './Maybe'
import { EmptyState } from './states'

/**
 * A ranked list with a bar per row; the bar is the share of the known cost of the list.
 * With `showPartial`, a row that has unpriced calls says "partial" and counts them.
 */
export function RankedList({
  label,
  rows,
  total,
  link,
  showPartial = false,
}: {
  label: string
  rows: GroupTotal[]
  total: number
  link?: (group: string) => ReactNode
  showPartial?: boolean
}) {
  if (rows.length === 0) return <EmptyState>No calls in this range.</EmptyState>
  const max = Math.max(1, ...rows.map((r) => r.cost ?? 0))
  return (
    <ol className="ranked" aria-label={label}>
      {rows.map((r) => {
        const name = formatText(r.group)
        const share = r.cost !== null && total > 0 ? `${((r.cost / total) * 100).toFixed(0)}%` : ''
        return (
          <li key={r.group}>
            <span className="ranked-name">{link && r.group !== '' ? link(r.group) : <Tag text={name} className="mono" />}</span>
            <span className="ranked-value num">
              <Maybe text={formatCostMicroUsd(r.cost)} />
              {share && <span className="dim"> · {share}</span>}
              {showPartial && r.unpriced > 0 && (
                <span className="dim">{` · partial: ${formatCount(r.unpriced)} unpriced`}</span>
              )}
            </span>
            <span className="ranked-bar" aria-hidden="true">
              <span style={{ width: `${(((r.cost ?? 0) / max) * 100).toFixed(1)}%` }} />
            </span>
          </li>
        )
      })}
    </ol>
  )
}
