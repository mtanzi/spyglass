/**
 * The theme choice (ADR 0053): follow the system setting, or force light or
 * dark. A forced choice is the data-theme attribute on <html>; "system" is no
 * attribute, so color-scheme: light dark in styles.css lets the browser
 * decide.
 * The choice is remembered per browser in localStorage. Storage can be
 * missing or throw (private windows, blocked site data), so every access is
 * guarded and "system" is the fallback.
 *
 * public/theme-init.js repeats readTheme and applyTheme as a classic script
 * that runs before first paint, because the CSP allows no inline script.
 * Keep the two in step.
 */
export type ThemeChoice = 'system' | 'light' | 'dark'

export const THEME_STORAGE_KEY = 'spyglass-theme'

export const THEME_CHOICES: readonly ThemeChoice[] = ['system', 'light', 'dark']

/** The stored choice, or "system" when nothing valid is stored or storage fails. */
export function readTheme(): ThemeChoice {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // Storage is unavailable: follow the system.
  }
  return 'system'
}

/** Remembers the choice. "system" clears the stored value. A storage failure is ignored. */
export function saveTheme(choice: ThemeChoice): void {
  try {
    if (choice === 'system') window.localStorage.removeItem(THEME_STORAGE_KEY)
    else window.localStorage.setItem(THEME_STORAGE_KEY, choice)
  } catch {
    // Storage is unavailable: the choice lasts for this page only.
  }
}

/** Sets or clears the data-theme attribute on <html>. */
export function applyTheme(choice: ThemeChoice, root: HTMLElement = document.documentElement): void {
  if (choice === 'system') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', choice)
}

const DARK_QUERY = '(prefers-color-scheme: dark)'

/** The scheme the system asks for, or null when matchMedia is missing. */
export function systemScheme(): 'light' | 'dark' | null {
  if (typeof window.matchMedia !== 'function') return null
  return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

/** Calls onChange when the system scheme changes. Returns the unsubscribe function. */
export function subscribeSystemScheme(onChange: () => void): () => void {
  if (typeof window.matchMedia !== 'function') return () => {}
  const query = window.matchMedia(DARK_QUERY)
  query.addEventListener('change', onChange)
  return () => query.removeEventListener('change', onChange)
}
