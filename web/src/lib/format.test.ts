import { expect, test } from 'vitest'
import {
  UNKNOWN,
  formatBytes,
  formatCappedCount,
  formatCostMicroUsd,
  formatCount,
  formatDurationMs,
  formatText,
  formatTimeSplit,
  zeroAsUnknown,
} from './format'

test('null and undefined show unknown, never zero', () => {
  for (const f of [formatCount, formatCostMicroUsd, formatDurationMs, formatBytes]) {
    expect(f(null)).toBe(UNKNOWN)
    expect(f(undefined)).toBe(UNKNOWN)
  }
  expect(formatText(null)).toBe(UNKNOWN)
  expect(formatText('')).toBe(UNKNOWN)
})

test('a real zero is shown as zero', () => {
  expect(formatCount(0)).toBe('0')
  expect(formatCostMicroUsd(0)).toBe('$0.00')
  expect(formatDurationMs(0)).toBe('0 ms')
  expect(formatBytes(0)).toBe('0 B')
})

test('zeroAsUnknown turns a not-recorded 0 into unknown', () => {
  expect(formatDurationMs(zeroAsUnknown(0))).toBe(UNKNOWN)
  expect(formatDurationMs(zeroAsUnknown(120))).toBe('120 ms')
})

test('formats values', () => {
  expect(formatCount(1234567)).toBe('1,234,567')
  expect(formatCostMicroUsd(1_500_000)).toBe('$1.50')
  expect(formatCostMicroUsd(1234)).toBe('$0.001234')
  expect(formatDurationMs(1500)).toBe('1.50 s')
  expect(formatDurationMs(125_000)).toBe('2 min 5 s')
  expect(formatBytes(2048)).toBe('2.0 KiB')
  expect(formatCappedCount(10000, true)).toBe('10,000+')
  expect(formatCappedCount(42, false)).toBe('42')
})

test('formatTimeSplit shows each part as a share of model plus tool time', () => {
  expect(formatTimeSplit(42_000, 28_000, false)).toBe('LLM 42.00 s (60%) · tools 28.00 s (40%)')
  expect(formatTimeSplit(42_000, 28_000, true)).toBe('LLM 42.00 s (60%) · tools 28.00 s est. (40%)')
  expect(formatTimeSplit(500, 0, false)).toBe('LLM 500 ms (100%) · tools 0 ms (0%)')
  expect(formatTimeSplit(0, 0, false)).toBe(UNKNOWN)
})
