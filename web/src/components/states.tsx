import { errorMessage } from '../api/errors'

/**
 * The one loading state of every page: a small spinner and a label. It is not
 * a live region: announcing every interstitial "Loading…" is noise. The table
 * that is loading carries `aria-busy` instead.
 */
export function LoadingState({ label = 'Loading' }: { label?: string }) {
  return (
    <p className="state state-loading">
      {label}…
    </p>
  )
}

/** The one empty state of every page: a muted, centred message. */
export function EmptyState({ children }: { children: React.ReactNode }) {
  return <p className="state state-empty">{children}</p>
}

/** Inline message for an API or network error (ADR 0030). */
export function ErrorMessage({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div className="state error" role="alert">
      <span>{errorMessage(error)}</span>
      {onRetry && (
        <button type="button" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  )
}
