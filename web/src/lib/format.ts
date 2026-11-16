/** Text shown where a value is not known. Unknown is not zero. */
export const UNKNOWN = 'unknown'

const integer = new Intl.NumberFormat('en-US')

/** Formats a count. null and undefined show "unknown", never 0. */
export function formatCount(value: number | null | undefined): string {
  return value == null ? UNKNOWN : integer.format(value)
}

/** Formats micro-USD as dollars. null (no price matched) shows "unknown". */
export function formatCostMicroUsd(value: number | null | undefined): string {
  if (value == null) return UNKNOWN
  const usd = value / 1_000_000
  return `$${usd.toFixed(usd !== 0 && Math.abs(usd) < 0.01 ? 6 : 2)}`
}

/** Formats milliseconds as a short duration. */
export function formatDurationMs(value: number | null | undefined): string {
  if (value == null) return UNKNOWN
  if (value < 1000) return `${integer.format(value)} ms`
  if (value < 60_000) return `${(value / 1000).toFixed(2)} s`
  const minutes = Math.floor(value / 60_000)
  const seconds = Math.round((value % 60_000) / 1000)
  return `${minutes} min ${seconds} s`
}

/** Formats a byte size. */
export function formatBytes(value: number | null | undefined): string {
  if (value == null) return UNKNOWN
  if (value < 1024) return `${integer.format(value)} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`
}

/** Formats a count that the API caps (count_capped), e.g. "10,000+". */
export function formatCappedCount(count: number, capped: boolean): string {
  return capped ? `${integer.format(count)}+` : integer.format(count)
}

/** Shows a string, or "unknown" for null, undefined and the empty string. */
export function formatText(value: string | null | undefined): string {
  return value == null || value === '' ? UNKNOWN : value
}

/**
 * Some call fields use 0 for "not recorded" (for example ttft_ms on a
 * non-streamed call). Pass them through this before formatting.
 */
export function zeroAsUnknown(value: number | null | undefined): number | null {
  return value == null || value === 0 ? null : value
}

/** Why a client_passthrough cost is shown as API-equivalent. */
export const PASSTHROUGH_NOTE = 'client_passthrough: the plan bills the person, not per token'
