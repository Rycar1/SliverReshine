import { describe, it, expect } from 'vitest'
import { base64ToBytes, bytesToText, bytesToBase64, fileToBase64 } from '../binary'

// binary.ts is the single home for the base64 helpers the console used to copy
// per file -- eight local copies were collapsed into it. Every caller hands it
// payloads that came off the wire, so the contract pinned here is what those
// call sites rely on.

const bytesOf = (b: ArrayBuffer) => Array.from(new Uint8Array(b))

describe('base64ToBytes', () => {
  it('decodes to the exact byte values', () => {
    expect(bytesOf(base64ToBytes('AAEC'))).toEqual([0, 1, 2])
  })

  it('decodes an empty string to an empty buffer', () => {
    expect(bytesOf(base64ToBytes(''))).toEqual([])
  })

  it('preserves bytes above 0x7f', () => {
    // '/w==' is 0xFF; the charCodeAt loop must not sign-extend or truncate it.
    expect(bytesOf(base64ToBytes('/w=='))).toEqual([0xff])
  })

  it('throws on input that is not base64', () => {
    // The throw is the signal bytesToText uses to fall back to the raw value.
    expect(() => base64ToBytes('!!!')).toThrow()
  })
})

describe('bytesToText', () => {
  it('returns an empty string for an absent payload', () => {
    expect(bytesToText(undefined)).toBe('')
    expect(bytesToText('')).toBe('')
  })

  it('decodes UTF-8, including multi-byte characters', () => {
    // '5L2g5aW9' is the UTF-8 encoding of the two-character string below.
    expect(bytesToText('5L2g5aW9')).toBe('你好')
  })

  it('shows the raw value when the payload is not base64', () => {
    // The console must show something rather than nothing.
    expect(bytesToText('not base64!')).toBe('not base64!')
  })

  it('does not throw on undecodable bytes', () => {
    // fatal:false -- a lone 0xFF byte becomes U+FFFD instead of an exception.
    expect(() => bytesToText('//8=')).not.toThrow()
  })
})

describe('bytesToBase64', () => {
  it('encodes known bytes', () => {
    expect(bytesToBase64(new Uint8Array([0, 1, 2]))).toBe('AAEC')
  })

  it('encodes an empty array to an empty string', () => {
    expect(bytesToBase64(new Uint8Array(0))).toBe('')
  })

  it('handles a payload larger than the 0x8000 chunk', () => {
    // The chunk loop exists because String.fromCharCode(...bytes) overflows the
    // call stack past roughly 64k arguments; this payload crosses that boundary.
    const n = 0x8000 * 2 + 7
    const input = new Uint8Array(n)
    for (let i = 0; i < n; i++) input[i] = i & 0xff
    expect(bytesOf(base64ToBytes(bytesToBase64(input)))).toEqual(Array.from(input))
  })

  it('round-trips through base64ToBytes', () => {
    const input = new Uint8Array([0, 127, 128, 255, 1, 2, 3])
    expect(bytesOf(base64ToBytes(bytesToBase64(input)))).toEqual(Array.from(input))
  })
})

describe('fileToBase64', () => {
  it('reads a file and strips the data-URL prefix', async () => {
    const file = new File(['hi'], 'note.txt', { type: 'text/plain' })
    await expect(fileToBase64(file)).resolves.toBe('aGk=')
  })

  it('resolves to an empty string for an empty file', async () => {
    const file = new File([], 'empty.bin')
    await expect(fileToBase64(file)).resolves.toBe('')
  })
})
