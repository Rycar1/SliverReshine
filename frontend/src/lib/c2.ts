export interface C2UrlParts {
  proto: string
  host: string
  port: string
}

// Splits a C2 URL like "mtls://1.2.3.4:8888" into its protocol, host and
// port. Mirrors how the backend assembles C2 URLs (see buildC2URL in
// backend/internal/sliver/implants.go).
export function parseC2Url(url: string, fallbackProto = 'mtls'): C2UrlParts {
  const sep = '://'
  const idx = url.indexOf(sep)
  if (idx === -1) {
    const [host, ...portParts] = url.split(':')
    return { proto: fallbackProto, host: host || '', port: portParts.join(':') }
  }
  const rawProto = url.slice(0, idx)
  const rest = url.slice(idx + sep.length)
  const [host, ...portParts] = rest.split(':')
  return { proto: rawProto || fallbackProto, host: host || '', port: portParts.join(':') }
}
