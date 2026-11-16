import { useEffect, useState } from 'react'

const DEFAULT_REM = 16

/** The root font size in px: the reader's text-size setting, 16 by default. */
export function readRem(): number {
  if (typeof document === 'undefined') return DEFAULT_REM
  const size = parseFloat(getComputedStyle(document.documentElement).fontSize)
  return Number.isFinite(size) && size > 0 ? size : DEFAULT_REM
}

/**
 * The root font size in px, for the virtualised lists, which need row heights
 * as numbers. A row is a number of rem, so it follows the reader's text size
 * (ADR 0058). The size is read again on resize and when the tab regains focus,
 * which is when a change of the browser's text-size setting shows up.
 */
export function useRem(): number {
  const [rem, setRem] = useState(readRem)
  useEffect(() => {
    const update = () => setRem(readRem())
    update()
    window.addEventListener('resize', update)
    window.addEventListener('focus', update)
    return () => {
      window.removeEventListener('resize', update)
      window.removeEventListener('focus', update)
    }
  }, [])
  return rem
}
