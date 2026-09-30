export function base64ToBytes(b64: string): ArrayBuffer {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

export function triggerDownload(name: string, bytes: ArrayBuffer) {
  const blob = new Blob([bytes], { type: 'application/octet-stream' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name.split(/[\\/]/).pop() || name
  a.click()
  URL.revokeObjectURL(url)
}

export function bytesToText(b64: string): string {
  try {
    const buf = base64ToBytes(b64)
    return new TextDecoder('utf-8', { fatal: false }).decode(buf)
  } catch {
    return b64
  }
}

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}
