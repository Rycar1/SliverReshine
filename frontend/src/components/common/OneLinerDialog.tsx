import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { Job, MultiOneLinerResult } from '../../lib/types'

interface Props {
  /** The listener to build for. Null closes the dialog. */
  job: Job | null
  onClose: () => void
}

/**
 * One-click staging commands for an existing listener.
 *
 * This exists because the one-liner panel above the listener list makes the
 * operator pick the listener from a dropdown, which is the wrong way round for
 * the common case: you are looking at a listener row and want the command for
 * *that* listener. The row is already the selection.
 *
 * Both platforms are built together because the operator usually does not know
 * yet which the target runs, and finding out costs another wait. That means two
 * implant builds, so:
 *
 *   - It is a button rather than automatic. A table that fired two builds per
 *     row on render would be a denial of service against the operator's CPU.
 *   - Both are built concurrently, so the wait is one build rather than two.
 *   - A failure in one is reported next to that platform instead of failing the
 *     dialog, because the other command may be perfectly usable.
 *
 * Only HTTP-family listeners can serve a stage, so for anything else the button
 * is disabled with the reason shown rather than offered and then failing.
 */
export default function OneLinerDialog({ job, onClose }: Props) {
  const { t } = useTranslation()
  const [results, setResults] = useState<MultiOneLinerResult[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')
  const [advanced, setAdvanced] = useState(false)

  // Reset whenever the dialog is pointed at a different listener. Reusing the
  // previous listener's commands would hand the operator a stage that calls
  // back to a different port -- the exact mismatch this feature exists to stop.
  useEffect(() => {
    setResults([])
    setError('')
    setCopied('')
    setAdvanced(false)
  }, [job?.ID])

  if (!job) return null

  const canStage = (job.Name || '').toLowerCase() === 'http' || (job.Name || '').toLowerCase() === 'https'

  const generate = async () => {
    setBusy(true)
    setError('')
    setResults([])
    try {
      const res = await api.oneLinerAll({
        job_id: job.ID,
        platforms: ['windows', 'linux'],
      })
      setResults(res.results || [])
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const copy = async (text: string, tag: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(tag)
      window.setTimeout(() => setCopied(''), 1500)
    } catch {
      // Clipboard can be refused; the textarea stays selectable.
      setCopied('')
    }
  }

  const copyCommand = (command: string) => copy(command, 'cmd-' + command)

  return (
    <div className="modal-overlay confirm-overlay" onClick={onClose}>
      <div
        className="modal"
        style={{ maxWidth: 720, width: '90vw' }}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={t('oneliner.dialogTitle')}
      >
        <div className="modal-header">
          <div className="modal-title">
            {t('oneliner.dialogTitle')} — {job.Name}:{job.Port}
          </div>
        </div>

        <div className="modal-body">
          {!canStage ? (
            <div className="error-banner">
              {t('oneliner.notStageable', { name: job.Name })}
            </div>
          ) : (
            <>
              <div className="page-sub" style={{ marginBottom: 12 }}>
                {t('oneliner.dialogHint')}
              </div>

              {error && <div className="error-banner">{error}</div>}

              {results.length === 0 && !busy && (
                <button type="button" className="btn primary" onClick={generate}>
                  {t('oneliner.dialogGenerate')}
                </button>
              )}

              {busy && (
                <div className="page-sub">{t('oneliner.dialogBuilding')}</div>
              )}

              {results.map((r) => (
                <div key={r.platform} style={{ marginBottom: 18 }}>
                  <div className="side-row" style={{ marginBottom: 6 }}>
                    <span className="side-label">
                      {r.platform === 'windows' ? 'Windows' : r.platform === 'linux' ? 'Linux' : r.platform}
                    </span>
                    {r.command && (
                      <button
                        type="button"
                        className="btn sm"
                        onClick={() => copyCommand(r.command)}
                      >
                        {copied === 'cmd-' + r.command ? t('common.copied') : t('common.copy')}
                      </button>
                    )}
                  </div>

                  {r.error ? (
                    <div className="error-banner">{r.error}</div>
                  ) : (
                    <>
                      <textarea
                        readOnly
                        value={r.command}
                        rows={2}
                        style={{
                          width: '100%',
                          fontFamily: 'JetBrains Mono, Fira Code, Consolas, monospace',
                          fontSize: 12,
                          background: 'var(--surface-2, #16161a)',
                          color: 'inherit',
                          border: '1px solid var(--border, #2a2a33)',
                          borderRadius: 4,
                          padding: 8,
                          resize: 'vertical',
                        }}
                        onFocus={(e) => e.currentTarget.select()}
                      />
                      <div className="side-row">
                        <span className="side-label">{t('oneliner.builtAs')}</span>
                        <span className="side-value mono">{r.staged_as}</span>
                      </div>
                    </>
                  )}
                </div>
              ))}

              {results.some((r) => r.command) && (
                <details open={advanced} onToggle={(e) => setAdvanced((e.target as HTMLDetailsElement).open)}>
                  <summary style={{ cursor: 'pointer', fontSize: 12 }}>
                    {t('oneliner.dialogDetails')}
                  </summary>
                  {results
                    .filter((r) => r.command)
                    .map((r) => (
                      <div key={r.platform} style={{ marginTop: 10 }}>
                        <div className="side-row">
                          <span className="side-label">{r.platform}</span>
                          <span className="side-value mono">{r.url}</span>
                        </div>
                      </div>
                    ))}
                </details>
              )}

              {results.length > 0 && (
                <button
                  type="button"
                  className="btn"
                  style={{ marginTop: 12 }}
                  onClick={generate}
                  disabled={busy}
                >
                  {t('oneliner.dialogRebuild')}
                </button>
              )}
            </>
          )}
        </div>

        <div className="modal-footer">
          <button type="button" className="btn" onClick={onClose}>
            {t('common.close')}
          </button>
        </div>
      </div>
    </div>
  )
}
