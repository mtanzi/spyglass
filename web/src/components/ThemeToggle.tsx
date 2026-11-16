import { useEffect, useState, useSyncExternalStore } from 'react'
import {
  applyTheme,
  readTheme,
  saveTheme,
  subscribeSystemScheme,
  systemScheme,
  THEME_CHOICES,
  THEME_STORAGE_KEY,
  type ThemeChoice,
} from '../lib/theme'

const LABELS: Record<ThemeChoice, string> = { system: 'System', light: 'Light', dark: 'Dark' }

/**
 * The theme switch in the sidebar footer (ADR 0053): System, Light or Dark as
 * a segmented control. The pressed button is the current choice. The choice
 * is applied to <html> and remembered per browser. The System button names
 * the scheme the system uses, for example "System (dark)", and follows a
 * change of the system setting. A change of the stored choice in another tab
 * arrives as a storage event, so every tab stays in step.
 */
export function ThemeToggle() {
  const [choice, setChoice] = useState<ThemeChoice>(readTheme)
  const scheme = useSyncExternalStore(subscribeSystemScheme, systemScheme)
  useEffect(() => applyTheme(choice), [choice])
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      // A null key means the whole storage was cleared.
      if (e.key === null || e.key === THEME_STORAGE_KEY) setChoice(readTheme())
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [])
  const choose = (next: ThemeChoice) => {
    saveTheme(next)
    setChoice(next)
  }
  return (
    <div className="segmented theme-toggle" role="group" aria-label="Theme">
      {THEME_CHOICES.map((c) => (
        <button key={c} type="button" aria-pressed={choice === c} onClick={() => choose(c)}>
          {c === 'system' && scheme ? `${LABELS[c]} (${scheme})` : LABELS[c]}
        </button>
      ))}
    </div>
  )
}
