export const WS_MSG_DATA = 0x01
export const WS_MSG_RESIZE = 0x02
/**
 * The connection ended and retrying is reasonable: the tunnel stream could not
 * be opened, or the shell process ended. The terminal reconnects with backoff.
 */
export const WS_MSG_CLOSE = 0x03
/**
 * The terminal is over and retrying cannot help: the server refused to start
 * the shell, failed to stage a shell copy, or the operator asked to leave.
 *
 * This is separate from WS_MSG_CLOSE because the two need opposite handling.
 * Treating a fatal end as a dropped connection is what made a failed
 * shell-copy retry forever, re-running the upload that had just failed, and
 * made "exit" reopen the terminal the operator was trying to close.
 */
export const WS_MSG_FATAL = 0x04

export interface WsFrame {
  type: number
  payload: Uint8Array
}

export function encodeFrame(type: number, payload: string | Uint8Array): ArrayBuffer {
  const bytes = typeof payload === 'string' ? new TextEncoder().encode(payload) : payload
  const buf = new ArrayBuffer(5 + bytes.length)
  const view = new Uint8Array(buf)
  view[0] = type
  new DataView(buf).setUint32(1, bytes.length, false)
  view.set(bytes, 5)
  return buf
}

export function decodeFrame(data: ArrayBuffer | string | unknown): WsFrame | null {
  if (typeof data === 'string') {
    const bytes = new TextEncoder().encode(data)
    if (bytes.length < 5) return null
    const type = bytes[0]
    const len =
      (bytes[1] << 24) | (bytes[2] << 16) | (bytes[3] << 8) | bytes[4]
    const payload = bytes.subarray(5, 5 + Math.min(len, bytes.length - 5))
    return { type, payload }
  }
  if (data instanceof ArrayBuffer) {
    if (data.byteLength < 5) return null
    const view = new DataView(data)
    const type = view.getUint8(0)
    const len = view.getUint32(1, false)
    const payload = new Uint8Array(data, 5, Math.min(len, data.byteLength - 5))
    return { type, payload }
  }
  return null
}
