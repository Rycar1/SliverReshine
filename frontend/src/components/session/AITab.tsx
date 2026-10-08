import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { AICollectResult, AIStatus } from '../../lib/types'
import { useToast } from '../common/Toast'
import AIStepsView, { applyAIEvent, emptyAIStream, type AIStreamState } from './AIStepsView'

/**
 * AITab lets the configured model probe a live session for credentials and
 * useful files.
 *
 * The model only ever proposes commands; anything it extracts is written into
 * the loot and credential vaults unless the operator turns storing off.
 *
 * The run is streamed rather than awaited: the operator watches the model
 * reason and run each command as it happens, which is the only way to tell a
 * working run from one that is stuck.
 */
export default function AITab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const toast = useToast()

  const [status, setStatus] = useState<AIStatus | null>(null)
  const [statusError, setStatusError] = useState('')
  // The live read-only policy. It is a deployment setting, so the toggle
  // below writes it back to the server rather than only changing the form.
  const [readOnly, setReadOnly] = useState(true)
  const [savingPolicy, setSavingPolicy] = useState(false)

  const [objective, setObjective] = useState('')
  const [noStore, setNoStore] = useState(false)
  // A second model pass reviews the extraction phase's findings and drops the
  // duplicates and the entries with no value before anything is filed.
  const [filterFindings, setFilterFindings] = useState(false)
  const [running, setRunning] = useState(false)
  const [stream, setStream] = useState<AIStreamState>(emptyAIStream)
  const [result, setResult] = useState<AICollectResult | null>(null)
  const [error, setError] = useState('')
  // The stream callback fires many times per run; a ref keeps the latest state
  // in the accumulator without making the run callback depend on it.
  const streamRef = useRef<AIStreamState>(emptyAIStream)

  const loadStatus = useCallback(async () => {
    setStatusError('')
    try {
      const s = await api.aiStatus()
      setStatus(s)
      setReadOnly(s.readOnly)
    } catch (e) {
      setStatusError((e as Error).message)
    }
  }, [])

  useEffect(() => {
    loadStatus()
  }, [loadStatus])

  // Persist the policy. It is a server setting, so a successful save is
  // reflected back from the response rather than assumed locally.
  const toggleReadOnly = useCallback(
    async (next: boolean) => {
      setSavingPolicy(true)
      setError('')
      try {
        const saved = await api.aiSettingsUpdate({ readOnly: next })
        setReadOnly(saved.readOnly)
        setStatus((prev) => (prev ? { ...prev, readOnly: saved.readOnly } : prev))
        toast.push('success', t('ai.readOnlySaved'))
      } catch (e) {
        setError((e as Error).message)
      } finally {
        setSavingPolicy(false)
      }
    },
    [t, toast],
  )

  const run = useCallback(async () => {
    setRunning(true)
    setError('')
    setResult(null)
    streamRef.current = emptyAIStream
    setStream(emptyAIStream)
    try {
      const res = await api.aiCollectStream(
        sessionId,
        {
          objective: objective.trim() || undefined,
          no_store: noStore,
          filter_findings: filterFindings,
        },
        (ev) => {
          streamRef.current = applyAIEvent(streamRef.current, ev)
          setStream(streamRef.current)
        },
      )
      setResult(res)
      const stored = res.stored?.length || 0
      if (stored > 0) toast.push('success', t('ai.stored') + ': ' + stored)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }, [sessionId, objective, noStore, filterFindings, t, toast])

  const configured = !!status?.configured
  const shown = result?.steps?.length ? { ...stream, steps: result.steps } : stream

  return (
    <>
      <div className="card">
        <div className="card-title">{t('ai.title')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>{t('ai.hint')}</p>

        {statusError && <div className="error-banner" style={{ marginBottom: 10 }}>{statusError}</div>}

        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 10 }}>
          <span className={'badge ' + (configured ? 'green' : 'yellow')}>
            {configured ? t('ai.enabled') : t('ai.disabled')}
          </span>
          {status?.model && <span className="badge gray">{t('ai.model')}: {status.model}</span>}
          {status && (
            <span className={'badge ' + (status.hasKey ? 'green' : 'red')}>
              {t('ai.key')}: {status.hasKey ? t('ai.keySet') : t('ai.keyMissing')}
            </span>
          )}
        </div>

        {status && !readOnly && <div className="alert error" style={{ marginBottom: 10 }}>{t('ai.readOnlyOff')}</div>}

        {status && !configured && <div className="alert">{t('ai.notConfigured')}</div>}

        {configured && (
          <>
            <div className="pf-form">
              <div className="field" style={{ flex: 2 }}>
                <label htmlFor="ai-objective">{t('ai.objective')}</label>
                <input
                  id="ai-objective"
                  value={objective}
                  placeholder={t('ai.objectiveHint')}
                  onChange={(e) => setObjective(e.target.value)}
                />
              </div>
              <div className="field">
                <label htmlFor="ai-read-only" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input
                    id="ai-read-only"
                    type="checkbox"
                    checked={readOnly}
                    disabled={savingPolicy}
                    onChange={(e) => toggleReadOnly(e.target.checked)}
                  />
                  {t('ai.readOnly')}
                </label>
              </div>
              <div className="field">
                <label htmlFor="ai-no-store" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input
                    id="ai-no-store"
                    type="checkbox"
                    checked={noStore}
                    onChange={(e) => setNoStore(e.target.checked)}
                  />
                  {t('ai.noStore')}
                </label>
              </div>
              <div className="field">
                <label htmlFor="ai-filter-findings" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input
                    id="ai-filter-findings"
                    type="checkbox"
                    checked={filterFindings}
                    onChange={(e) => setFilterFindings(e.target.checked)}
                  />
                  {t('ai.filterFindings')}
                </label>
              </div>
            </div>
            <p className="page-sub" style={{ margin: '8px 0 0' }}>{t('ai.readOnlyHint')}</p>
            <p className="page-sub" style={{ margin: '4px 0 0' }}>{t('ai.filterFindingsHint')}</p>
            <div className="toolbar" style={{ marginTop: 10 }}>
              <button type="button" className="btn primary" onClick={run} disabled={running}>
                {running ? t('ai.running') : t('ai.run')}
              </button>
            </div>
          </>
        )}

        {error && <div className="error-banner" style={{ marginTop: 10 }}>{error}</div>}
      </div>

      {(running || shown.steps.length > 0 || shown.summary || result) && (
        <AIStepsView
          steps={shown.steps}
          running={running}
          live={{ thinking: shown.thinking, answer: shown.answer, command: shown.command, reason: shown.reason }}
          summary={shown.summary}
          badges={
            <>
              {result?.model && <span className="badge gray">{t('ai.model')}: {result.model}</span>}
              {shown.status && <span className="badge gray">{shown.status}</span>}
              {result?.stopped && <span className="badge yellow">{t('ai.stopped')}: {result.stopped}</span>}
            </>
          }
        />
      )}

      {result && <AICollectOutcome result={result} />}
    </>
  )
}

/** What a collection pass found and where it put it. */
function AICollectOutcome({ result }: { result: AICollectResult }) {
  const { t } = useTranslation()
  const findings = result.findings || []
  const stored = result.stored || []
  const skipped = result.skipped || []
  const filtered = result.filtered || []

  if (findings.length === 0 && stored.length === 0 && skipped.length === 0 && filtered.length === 0) {
    return null
  }

  return (
    <div className="card" style={{ marginTop: 14 }}>
      {findings.length > 0 && (
        <div>
          <div className="card-title" style={{ fontSize: 13 }}>{t('ai.findings')}</div>
          <table className="data">
            <thead>
              <tr>
                <th>{t('ai.kind')}</th>
                <th>{t('ai.name')}</th>
                <th>{t('ai.username')}</th>
                <th>{t('ai.secret')}</th>
                <th>{t('ai.source')}</th>
              </tr>
            </thead>
            <tbody>
              {findings.map((f, i) => (
                <tr key={i}>
                  <td><span className="badge gray">{f.kind || '-'}</span></td>
                  <td>{f.name || '-'}</td>
                  <td>{f.username || '-'}</td>
                  <td className="mono" style={{ maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {f.secret || f.content || '-'}
                  </td>
                  <td className="mono" style={{ fontSize: 12, opacity: 0.75 }}>{f.source || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {stored.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div className="card-title" style={{ fontSize: 13 }}>{t('ai.stored')}</div>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            {stored.map((s, i) => (
              <span key={i} className="badge green" title={s.id || undefined}>{s.kind}: {s.name}</span>
            ))}
          </div>
        </div>
      )}

      {filtered.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div className="card-title" style={{ fontSize: 13 }}>{t('ai.filtered')}</div>
          <ul style={{ margin: 0, paddingLeft: 18 }}>
            {filtered.map((f, i) => (
              <li key={i} style={{ fontSize: 12 }}>
                <span className="badge gray">{f.kind || '-'}</span>{' '}
                <span className="mono">{f.name || '-'}</span>
                {' — '}
                <span style={{ opacity: 0.75 }}>{f.reason || '-'}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {skipped.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div className="card-title" style={{ fontSize: 13 }}>{t('ai.skipped')}</div>
          <ul style={{ margin: 0, paddingLeft: 18 }}>
            {skipped.map((s, i) => (
              <li key={i} className="mono" style={{ fontSize: 12 }}>{s}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
