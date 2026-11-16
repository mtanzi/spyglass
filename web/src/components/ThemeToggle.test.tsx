import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import initScript from '../../public/theme-init.js?raw'
import { THEME_STORAGE_KEY } from '../lib/theme'
import { ThemeToggle } from './ThemeToggle'

const root = document.documentElement

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.localStorage.clear()
  root.removeAttribute('data-theme')
})

type Listener = () => void

/** Stubs matchMedia so the system scheme is dark or light, and lets a test flip it. */
function stubSystemScheme(initial: 'light' | 'dark') {
  let dark = initial === 'dark'
  const listeners = new Set<Listener>()
  vi.stubGlobal('matchMedia', (query: string) => ({
    media: query,
    get matches() {
      return dark
    },
    addEventListener: (_: string, l: Listener) => listeners.add(l),
    removeEventListener: (_: string, l: Listener) => listeners.delete(l),
  }))
  return (next: 'light' | 'dark') => {
    dark = next === 'dark'
    act(() => listeners.forEach((l) => l()))
  }
}

function pressed(): string[] {
  return screen
    .getAllByRole('button')
    .filter((b) => b.getAttribute('aria-pressed') === 'true')
    .map((b) => b.textContent ?? '')
}

test('follows the system by default', () => {
  render(<ThemeToggle />)
  expect(screen.getByRole('group', { name: 'Theme' })).toBeTruthy()
  expect(pressed()).toEqual(['System'])
  expect(root.hasAttribute('data-theme')).toBe(false)
})

test('a forced theme sets data-theme and is remembered; System clears both', () => {
  render(<ThemeToggle />)
  fireEvent.click(screen.getByRole('button', { name: 'Light' }))
  expect(pressed()).toEqual(['Light'])
  expect(root.getAttribute('data-theme')).toBe('light')
  expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')

  fireEvent.click(screen.getByRole('button', { name: 'Dark' }))
  expect(root.getAttribute('data-theme')).toBe('dark')
  expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')

  fireEvent.click(screen.getByRole('button', { name: /^System/ }))
  expect(pressed()).toEqual(['System'])
  expect(root.hasAttribute('data-theme')).toBe(false)
  expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBeNull()
})

test('the stored choice survives a reload', () => {
  const first = render(<ThemeToggle />)
  fireEvent.click(screen.getByRole('button', { name: 'Dark' }))
  first.unmount()
  root.removeAttribute('data-theme')

  render(<ThemeToggle />)
  expect(pressed()).toEqual(['Dark'])
  expect(root.getAttribute('data-theme')).toBe('dark')
})

test('an unknown stored value falls back to the system', () => {
  window.localStorage.setItem(THEME_STORAGE_KEY, 'sepia')
  render(<ThemeToggle />)
  expect(pressed()).toEqual(['System'])
  expect(root.hasAttribute('data-theme')).toBe(false)
})

test('blocked storage falls back to the system and the toggle still works', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('blocked')
  })
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('blocked')
  })
  render(<ThemeToggle />)
  expect(pressed()).toEqual(['System'])
  fireEvent.click(screen.getByRole('button', { name: 'Light' }))
  expect(pressed()).toEqual(['Light'])
  expect(root.getAttribute('data-theme')).toBe('light')
})

test('the System button names the scheme in use and follows the system', () => {
  const setSystem = stubSystemScheme('dark')
  render(<ThemeToggle />)
  expect(pressed()).toEqual(['System (dark)'])
  setSystem('light')
  expect(pressed()).toEqual(['System (light)'])
  fireEvent.click(screen.getByRole('button', { name: 'Dark' }))
  expect(screen.getByRole('button', { name: 'System (light)' })).toBeTruthy()
})

test('another tab changing the choice updates this one', () => {
  render(<ThemeToggle />)
  expect(pressed()).toEqual(['System'])

  window.localStorage.setItem(THEME_STORAGE_KEY, 'dark')
  act(() => {
    window.dispatchEvent(new StorageEvent('storage', { key: THEME_STORAGE_KEY, newValue: 'dark' }))
  })
  expect(pressed()).toEqual(['Dark'])
  expect(root.getAttribute('data-theme')).toBe('dark')

  window.localStorage.removeItem(THEME_STORAGE_KEY)
  act(() => {
    window.dispatchEvent(new StorageEvent('storage', { key: THEME_STORAGE_KEY, newValue: null }))
  })
  expect(pressed()).toEqual(['System'])
  expect(root.hasAttribute('data-theme')).toBe(false)
})

test('a storage event for another key or a cleared storage is handled', () => {
  render(<ThemeToggle />)
  fireEvent.click(screen.getByRole('button', { name: 'Light' }))
  act(() => {
    window.dispatchEvent(new StorageEvent('storage', { key: 'other', newValue: 'dark' }))
  })
  expect(pressed()).toEqual(['Light'])

  window.localStorage.clear()
  act(() => {
    window.dispatchEvent(new StorageEvent('storage', { key: null }))
  })
  expect(pressed()).toEqual(['System'])
})

// public/theme-init.js runs before first paint and repeats the read logic.
const runInit = () => new Function(initScript)()

test.each([
  ['light', 'light'],
  ['dark', 'dark'],
  ['sepia', null],
])('the pre-paint script applies a stored %s as %s', (stored, applied) => {
  window.localStorage.setItem(THEME_STORAGE_KEY, stored)
  runInit()
  expect(root.getAttribute('data-theme')).toBe(applied)
})

test('the pre-paint script tolerates blocked storage', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('blocked')
  })
  expect(runInit).not.toThrow()
  expect(root.hasAttribute('data-theme')).toBe(false)
})
