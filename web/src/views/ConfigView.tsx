import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import type { ReactNode } from 'react'
import { readQueryOptions } from '../api/queries'
import { Tag } from '../components/Badge'
import { EmptyState, ErrorMessage, LoadingState } from '../components/states'
import { formatCount, formatText } from '../lib/format'
import { configKeys, configPricing, configProviders, formatPricePerMillion, isPassthrough } from './configData'

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section className="panel config-section" aria-labelledby={id}>
      <div className="panel-head">
        <h2 id={id}>{title}</h2>
      </div>
      {children}
    </section>
  )
}

/**
 * The running config, read-only (ADR 0035): providers, pricing and key names
 * with their disabled flag. The page renders only the named fields, so
 * nothing else in the response (a hash, a secret) can reach the screen.
 * Keys and prices change in the config file and take effect on SIGHUP.
 */
export function ConfigView() {
  const query = useQuery(readQueryOptions('/admin/config'))
  const providers = useMemo(() => configProviders(query.data), [query.data])
  const pricing = useMemo(() => configPricing(query.data), [query.data])
  const keys = useMemo(() => configKeys(query.data), [query.data])

  return (
    <section aria-labelledby="view-title" className="dashboard config-view">
      <div className="view-header">
        <h1 id="view-title" tabIndex={-1}>Config</h1>
        <button type="button" onClick={() => void query.refetch()}>
          Refresh
        </button>
      </div>
      <p className="notice config-help">
        This page shows the running config and cannot change it. To add, disable or rotate a key, or to change a
        provider or a price, edit the config file and send <code>SIGHUP</code> to the <code>serve</code> process
        (<code>kill -HUP &lt;pid&gt;</code>). Keys are never created in this UI, and no hash or credential is ever shown.
      </p>

      {query.isError ? (
        <ErrorMessage error={query.error} onRetry={() => void query.refetch()} />
      ) : query.isPending ? (
        <LoadingState label="Loading config" />
      ) : (
        <>
          <Section id="providers-title" title="Providers">
            {providers.length === 0 ? (
              <EmptyState>No providers are configured.</EmptyState>
            ) : (
              <div className="table-frame">
                <table className="grid-table">
                  <caption className="sr-only">Providers</caption>
                  <thead>
                    <tr>
                      <th scope="col">Name</th>
                      <th scope="col">Kind</th>
                      <th scope="col">Base URL</th>
                      <th scope="col">Auth mode</th>
                      <th scope="col" className="num">Idle timeout</th>
                    </tr>
                  </thead>
                  <tbody>
                    {providers.map((p) => (
                      <tr key={p.name}>
                        <th scope="row" className="mono">{p.name}</th>
                        <td>{p.kind}</td>
                        <td className="mono cell-clip">{formatText(p.base_url)}</td>
                        <td>
                          <Tag text={p.auth_mode} className="mono" />
                          {isPassthrough(p) && (
                            <small className="dim">
                              {' '}
                              cost is API-equivalent: the plan bills the person, not per token
                            </small>
                          )}
                        </td>
                        <td className="num">{p.idle_timeout}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Section>

          <Section id="pricing-title" title="Pricing">
            {pricing.length === 0 ? (
              <EmptyState>No prices are configured, so every call has an unknown cost.</EmptyState>
            ) : (
              <>
                <div className="table-frame">
                  <table className="grid-table">
                    <caption className="sr-only">Pricing in USD per million tokens</caption>
                    <thead>
                      <tr>
                        <th scope="col">Provider</th>
                        <th scope="col">Model</th>
                        <th scope="col">From</th>
                        <th scope="col" className="num">Input</th>
                        <th scope="col" className="num">Output</th>
                        <th scope="col" className="num">Cache read</th>
                        <th scope="col" className="num">Cache write 5m</th>
                        <th scope="col" className="num">Cache write 1h</th>
                      </tr>
                    </thead>
                    <tbody>
                      {pricing.map((p) => (
                        <tr key={`${p.provider}|${p.model}|${p.from}`}>
                          <th scope="row" className="mono">{p.provider}</th>
                          <td className="mono cell-clip">{p.model}</td>
                          <td className="num">{p.from}</td>
                          <td className="num">{formatPricePerMillion(p.price.input)}</td>
                          <td className="num">{formatPricePerMillion(p.price.output)}</td>
                          <td className="num">{formatPricePerMillion(p.price.cache_read)}</td>
                          <td className="num">{formatPricePerMillion(p.price.cache_write_5m)}</td>
                          <td className="num">{formatPricePerMillion(p.price.cache_write_1h)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="notice">USD per million tokens. A call whose model has no price shows its cost as unknown.</p>
              </>
            )}
          </Section>

          <Section id="keys-title" title="Keys">
            {keys.length === 0 ? (
              <EmptyState>No keys are configured.</EmptyState>
            ) : (
              <>
                <div className="table-frame">
                  <table className="grid-table">
                    <caption className="sr-only">Key names</caption>
                    <thead>
                      <tr>
                        <th scope="col">Name</th>
                        <th scope="col">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {keys.map((k) => (
                        <tr key={k.name}>
                          <th scope="row" className="mono">{k.name}</th>
                          <td>
                            {k.disabled ? (
                              <span className="badge badge-error">
                                <span aria-hidden="true">⊘ </span>disabled
                              </span>
                            ) : (
                              <span className="badge badge-ok">
                                <span aria-hidden="true">✓ </span>enabled
                              </span>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="notice">
                  {`${formatCount(keys.length)} ${keys.length === 1 ? 'key' : 'keys'}, ${formatCount(keys.filter((k) => k.disabled).length)} disabled. `}
                  Usage is grouped by key name, so renaming a key splits its history.
                </p>
              </>
            )}
          </Section>
        </>
      )}
    </section>
  )
}
