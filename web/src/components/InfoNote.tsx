import { useId } from 'react'

/**
 * A short visible label that opens a note. Use it for text that is too long to
 * show in place, instead of a `title` attribute, which keyboard and touch users
 * cannot reach. The note is a native `popover="auto"`: the browser closes it on
 * Escape or a click outside and gives focus back to the button. CSS anchors it
 * under the button where the browser supports that.
 */
export function InfoNote({ label, note, className }: { label: string; note: string; className?: string }) {
  const id = useId()
  // An anchor name is a CSS dashed ident: useId output has colons, so drop them.
  const anchor = `--note-${id.replace(/[^a-zA-Z0-9_-]/g, '')}`
  return (
    <span className={className ? `info-note ${className}` : 'info-note'}>
      <button
        type="button"
        popoverTarget={id}
        style={{ anchorName: anchor }}
        // A table row may be clickable; opening a note must not open the row.
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
      >
        {label}
      </button>
      <span id={id} role="note" className="info-note-panel" popover="auto" style={{ positionAnchor: anchor }}>
        {note}
      </span>
    </span>
  )
}
