import { describe, it, expect } from 'vitest'
import en from '../locales/en'
import zh from '../locales/zh'

function flattenKeys(obj: Record<string, unknown>, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) => {
    const path = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      return flattenKeys(v as Record<string, unknown>, path)
    }
    return [path]
  })
}

describe('i18n locales', () => {
  it('en and zh define the exact same keys', () => {
    const enKeys = flattenKeys(en).sort()
    const zhKeys = flattenKeys(zh).sort()
    expect(enKeys).toEqual(zhKeys)
  })

  it('en has no empty or placeholder values', () => {
    const values = flattenKeys(en).map((path) => path.split('.').reduce<unknown>((acc, p) => (acc as Record<string, unknown>)[p], en))
    for (const v of values) {
      expect(typeof v === 'string' ? v.trim() : '').not.toBe('')
    }
  })
})
