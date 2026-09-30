// OS-aware remote path helpers for the files browser. `sep` is the path
// separator of the session's OS ('\\' on Windows, '/' elsewhere).

export function joinPath(base: string, name: string, sep: string): string {
  return base.endsWith(sep) ? `${base}${name}` : `${base}${sep}${name}`
}

export function parentOf(p: string, sep: string): string {
  if (!p) return p
  const trimmed = p.endsWith(sep) && p.length > 1 ? p.slice(0, -1) : p
  if (!trimmed) return p
  const idx = trimmed.lastIndexOf(sep)
  if (idx <= 0) return trimmed[idx] === sep ? sep : '/'
  return trimmed.slice(0, idx)
}
