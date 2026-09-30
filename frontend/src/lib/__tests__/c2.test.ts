import { describe, it, expect } from 'vitest'
import { parseC2Url } from '../c2'

describe('parseC2Url', () => {
  it('splits a full C2 URL', () => {
    expect(parseC2Url('mtls://1.2.3.4:8888')).toEqual({ proto: 'mtls', host: '1.2.3.4', port: '8888' })
    expect(parseC2Url('https://example.com:8443')).toEqual({ proto: 'https', host: 'example.com', port: '8443' })
  })

  it('handles URLs without a port', () => {
    expect(parseC2Url('dns://example.com')).toEqual({ proto: 'dns', host: 'example.com', port: '' })
  })

  it('falls back to mtls when no protocol is present', () => {
    expect(parseC2Url('1.2.3.4:8888')).toEqual({ proto: 'mtls', host: '1.2.3.4', port: '8888' })
    expect(parseC2Url('')).toEqual({ proto: 'mtls', host: '', port: '' })
  })
})
