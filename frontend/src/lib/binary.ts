export function base64ToBytes(b64: string): ArrayBuffer {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

/**
 * Save bytes to the operator's machine.
 *
 * Accepts a Blob as well as an ArrayBuffer: the file-download endpoint streams
 * its response, so the caller already has a Blob and wrapping it in another one
 * would copy the whole file for no reason.
 */
export function triggerDownload(name: string, data: ArrayBuffer | Blob) {
  const blob = data instanceof Blob ? data : new Blob([data], { type: 'application/octet-stream' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name.split(/[\\/]/).pop() || name
  a.click()
  URL.revokeObjectURL(url)
}

/**
 * Decode a base64 payload as UTF-8 text.
 *
 * Returns '' when the payload is absent, and the input unchanged when it is
 * not valid base64 -- the console shows the raw value rather than nothing.
 */
export function bytesToText(b64?: string): string {
  if (!b64) return ''
  try {
    const buf = base64ToBytes(b64)
    return new TextDecoder('utf-8', { fatal: false }).decode(buf)
  } catch {
    return b64
  }
}

/** Read a File as base64, without the data-URL prefix. */
export function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      resolve(result.split(',')[1] || '')
    }
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}
