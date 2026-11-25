import type { ReactNode } from 'react'
import { ErrorMessage, LoadingState } from './states'

export function Panel({
  id,
  title,
  action,
  className,
  children,
}: {
  id: string
  title: string
  action?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <section className={className ? `panel ${className}` : 'panel'} aria-labelledby={id}>
      <div className="panel-head">
        <h2 id={id}>{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}

/** Loading, error and content of a panel from one query. */
export function QueryState({
  query,
  children,
}: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown }
  children: () => ReactNode
}) {
  if (query.isError) return <ErrorMessage error={query.error} onRetry={() => void query.refetch()} />
  if (query.isPending) return <LoadingState />
  return <>{children()}</>
}
