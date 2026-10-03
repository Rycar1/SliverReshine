// Small display formatters shared by pages and session tabs. Kept free of
// React and API imports so any module can use them.

/** Human-readable byte size, e.g. "1.5 KB". Renders 0 as "0 B". */
export function fmtSize(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(1)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
}

/**
 * Absolute local timestamp from a unix-seconds value, e.g. "1/2/2021, 3:04:05 AM".
 *
 * The relative "N minutes ago" formatters stay local to each page on purpose:
 * they disagree about whether a future timestamp is clamped to "just now", so
 * merging them would change what those pages render.
 */
export function fmtLocalTime(ts: number): string {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

/** Badge tone for a beacon task state. */
export function taskTone(state: string): 'green' | 'red' | 'yellow' {
  if (state === 'completed') return 'green'
  if (state === 'failed') return 'red'
  return 'yellow'
}
