import { describe, it, expect } from 'vitest'
import { fmtSize, fmtLocalTime, taskTone } from '../format'

describe('fmtSize', () => {
  it('renders each unit boundary', () => {
    expect(fmtSize(0)).toBe('0 B')
    expect(fmtSize(512)).toBe('512 B')
    expect(fmtSize(1024)).toBe('1.0 KB')
    expect(fmtSize(1536)).toBe('1.5 KB')
    expect(fmtSize(1024 * 1024)).toBe('1.0 MB')
    expect(fmtSize(1024 * 1024 * 1024)).toBe('1.00 GB')
  })
})

describe('fmtLocalTime', () => {
  it('renders a dash for a missing timestamp', () => {
    expect(fmtLocalTime(0)).toBe('-')
  })

  it('treats the input as unix seconds', () => {
    const ts = new Date(2021, 0, 2, 3, 4, 5).getTime() / 1000
    expect(fmtLocalTime(ts)).toBe(new Date(ts * 1000).toLocaleString())
  })
})

describe('taskTone', () => {
  it('maps terminal states and everything else', () => {
    expect(taskTone('completed')).toBe('green')
    expect(taskTone('failed')).toBe('red')
    expect(taskTone('queued')).toBe('yellow')
    expect(taskTone('running')).toBe('yellow')
    expect(taskTone('')).toBe('yellow')
  })
})
