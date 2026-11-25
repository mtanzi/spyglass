import { UNKNOWN } from '../lib/format'

/** Text that may be the "unknown" marker, which shows muted. */
export function Maybe({ text }: { text: string }) {
  return text === UNKNOWN ? <span className="unknown">{UNKNOWN}</span> : <>{text}</>
}
