import { describe, it, expect } from 'vitest'
import { encodeFrame, decodeFrame, WS_MSG_DATA, WS_MSG_RESIZE } from '../terminal'

describe('terminal frame protocol', () => {
  it('encodes data frames as [type][4-byte BE length][payload]', () => {
    const buf = encodeFrame(WS_MSG_DATA, 'ls -la')
    expect(buf.byteLength).toBe(5 + 'ls -la'.length)
    const view = new DataView(buf)
    expect(view.getUint8(0)).toBe(WS_MSG_DATA)
    expect(view.getUint32(1, false)).toBe(6)
    const bytes = new Uint8Array(buf, 5)
    expect(new TextDecoder().decode(bytes)).toBe('ls -la')
  })

  it('encodes resize frames from JSON payload', () => {
    const buf = encodeFrame(WS_MSG_RESIZE, JSON.stringify({ cols: 80, rows: 24 }))
    const frame = decodeFrame(buf)
    expect(frame).not.toBeNull()
    expect(frame!.type).toBe(WS_MSG_RESIZE)
    expect(JSON.parse(new TextDecoder().decode(frame!.payload))).toEqual({ cols: 80, rows: 24 })
  })

  it('decodes payloads with multibyte UTF-8 content', () => {
    const buf = encodeFrame(WS_MSG_DATA, '你好')
    const frame = decodeFrame(buf)
    expect(frame!.type).toBe(WS_MSG_DATA)
    expect(new TextDecoder().decode(frame!.payload)).toBe('你好')
  })

  it('returns null for truncated frames', () => {
    const buf = encodeFrame(WS_MSG_DATA, 'hello')
    const truncated = buf.slice(0, 3)
    expect(decodeFrame(truncated)).toBeNull()
  })
})
