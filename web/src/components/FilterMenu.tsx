import { useId, useState } from 'react'

/**
 * A button that opens a panel of filter fields. `activeCount` shows on the
 * button. The panel is a native `popover="auto"`: the browser puts it in the
 * top layer, closes it on Escape or a click outside, and gives focus back to
 * the button. CSS anchors it under the button where the browser supports that.
 */
export function FilterMenu({
  activeCount = 0,
  onClear,
  children,
}: {
  activeCount?: number
  onClear?: () => void
  children: React.ReactNode
}) {
  const panelId = useId()
  // An anchor name is a CSS dashed ident: useId output has colons, so drop them.
  const anchor = `--filters-${panelId.replace(/[^a-zA-Z0-9_-]/g, '')}`

  return (
    <div className="filter-menu">
      <button
        type="button"
        aria-label={activeCount > 0 ? `Filters (${activeCount})` : 'Filters'}
        popoverTarget={panelId}
        style={{ anchorName: anchor }}
      >
        Filters
        {activeCount > 0 && (
          <span className="filter-count" aria-hidden="true">
            {activeCount}
          </span>
        )}
      </button>
      {onClear && activeCount > 0 && (
        <button type="button" onClick={onClear}>
          Clear filters
        </button>
      )}
      <div
        id={panelId}
        className="filter-panel"
        role="group"
        aria-label="Filters"
        popover="auto"
        style={{ positionAnchor: anchor }}
      >
        {children}
      </div>
    </div>
  )
}

/** A repeated text filter: shows values as removable chips and adds one on Enter or Add. */
export function TextListFilter({
  label,
  values = [],
  onChange,
}: {
  label: string
  values?: string[]
  onChange: (next: string[] | undefined) => void
}) {
  const id = useId()
  const [draft, setDraft] = useState('')
  const add = () => {
    const v = draft.trim()
    if (v !== '' && !values.includes(v)) onChange([...values, v])
    setDraft('')
  }
  const remove = (v: string) => {
    const next = values.filter((x) => x !== v)
    onChange(next.length > 0 ? next : undefined)
  }
  return (
    <div className="filter-field">
      <label htmlFor={id}>{label}</label>
      <ul aria-label={`${label} values`}>
        {values.map((v) => (
          <li key={v}>
            {v}{' '}
            <button type="button" aria-label={`Remove ${label} ${v}`} onClick={() => remove(v)}>
              ×
            </button>
          </li>
        ))}
      </ul>
      <input
        id={id}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          // Enter that confirms an IME candidate (isComposing, or keyCode 229 in Safari) is not a request to add.
          if (e.key === 'Enter' && !e.nativeEvent.isComposing && e.keyCode !== 229) {
            e.preventDefault()
            add()
          }
        }}
      />
      <button type="button" onClick={add}>
        Add
      </button>
    </div>
  )
}

/** A yes/no/any filter for a boolean API parameter. */
export function BooleanFilter({
  label,
  value,
  onChange,
}: {
  label: string
  value: boolean | undefined
  onChange: (next: boolean | undefined) => void
}) {
  const id = useId()
  return (
    <div className="filter-field">
      <label htmlFor={id}>{label}</label>
      <select
        id={id}
        value={value === undefined ? '' : String(value)}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value === 'true')}
      >
        <option value="">Any</option>
        <option value="true">Yes</option>
        <option value="false">No</option>
      </select>
    </div>
  )
}

/** A non-negative integer threshold, such as "latency at least". */
export function NumberFilter({
  label,
  value,
  onChange,
}: {
  label: string
  value: number | undefined
  onChange: (next: number | undefined) => void
}) {
  const id = useId()
  return (
    <div className="filter-field">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        type="number"
        min={0}
        step={1}
        value={value ?? ''}
        onChange={(e) => {
          const n = e.target.valueAsNumber
          onChange(Number.isSafeInteger(n) && n >= 0 ? n : undefined)
        }}
      />
    </div>
  )
}

/** A filter that takes one text value. */
export function TextFilter({
  label,
  value,
  onChange,
}: {
  label: string
  value: string | undefined
  onChange: (next: string | undefined) => void
}) {
  const id = useId()
  return (
    <div className="filter-field">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value.trim() === '' ? undefined : e.target.value)}
      />
    </div>
  )
}
