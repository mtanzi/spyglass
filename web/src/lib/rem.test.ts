import { afterEach, expect, test } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { readRem, useRem } from './rem'

afterEach(() => {
  document.documentElement.style.fontSize = ''
})

test('reads the root font size and falls back to 16', () => {
  expect(readRem()).toBe(16)
  document.documentElement.style.fontSize = '32px'
  expect(readRem()).toBe(32)
})

test('useRem follows a resize', () => {
  const { result } = renderHook(() => useRem())
  expect(result.current).toBe(16)
  document.documentElement.style.fontSize = '24px'
  act(() => {
    window.dispatchEvent(new Event('resize'))
  })
  expect(result.current).toBe(24)
})
