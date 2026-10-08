import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { AIPrivescResult, AIStatus } from '../../lib/types'
import AIStepsView, { applyAIEvent, emptyAIStream, type AIStreamState } from './AIStepsView'

/**
 * PrivescTab runs one AI-driven privilege escalation attempt against a live
 * session.
 *
 * The run stages an enumeration helper (linpeas on Unix, winPEAS on Windows)
 * on the target, reads it, then works through escalation routes one command at
 * a time. Unlike credential collection, escalation defaults to the full shell:
 * a run that cannot change state cannot escalate, so the read-only allowlist is
 * an opt-in here rather than the default.
 *
 * The whole run is streamed, so the operator watches the model reason, sees
 * every command as it is chosen, and can tell a slow step from a stuck run.
 */
export default function PrivescTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()

  const [status, setStatus] = useState<AIStatus | null>(null)
  const [statusError, setStatusError] = useState('')

  const [objective, setObjective] = useState('')
  const [readOnly, setReadOnly] = useState(false)
  const [skipEnum, setSkipEnum] = useState(false)
  const [refreshTool, setRefreshTool] = useState(false)
  const [keepTool, setKeepTool] = useState(false)
  const [dryRun, setDryRun] = useState(false)

  const [running, setRunning] = useState(false)
  const [stream, setStream] = useState<AIStreamState>(emptyAIStream)
  const [result, setResult] = useState<AIPrivescResult | null>(null)
  const [error, setError] = useState('')
  const streamRef = useRef<AIStreamState>(emptyAIStream)

  const loadStatus = useCallback(async () => {
    setStatusError('')
    try {
      setStatus(await api.aiStatus())
    } catch (e) {
      setStatusError((e as Error).message)
    }
  }, [])

  useEffect(() => {
    loadStatus()
  }, [loadStatus])

  const run = useCallback(async () => {
    setRunning(true)
    setError('')
    setResult(null)
    streamRef.current = emptyAIStream
    setStream(emptyAIStream)
    try {
      const res = await api.aiPrivescStream(
        sessionId,
        {
          objective: objective.trim() || undefined,
          read_only: readOnly,
          dry_run: dryRun,
          skip_enum: skipEnum,
          refresh_tool: refreshTool,
          keep_tool: keepTool,
        },
        (ev) => {
          streamRef.current = applyAIEvent(streamRef.current, ev)
          setStream(streamRef.current)
        },
      )
      setResult(res)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }, [sessionId, objective, readOnly, dryRun, skipEnum, refreshTool, keepTool])

  const configured = !!status?.configured
  const shown = result?.steps?.length ? { ...stream, steps: result.steps } : stream

  return (
    <>
      <div className="card">
        <div className="card-title">{t('privesc.title')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>{t('privesc.hint')}</p>

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

        {status && !configured && <div className="alert">{t('ai.notConfigured')}</div>}

        {configured && (
          <>
            <div className="pf-form">
              <div className="field" style={{ flex: 2 }}>
                <label htmlFor="privesc-objective">{t('privesc.objective')}</label>
                <input
                  id="privesc-objective"
                  value={objective}
                  placeholder={t('privesc.objectiveHint')}
                  onChange={(e) => setObjective(e.target.value)}
                />
              </div>
            </div>

            <div className="pf-form" style={{ marginTop: 8 }}>
              <div className="field">
                <label htmlFor="privesc-read-only" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input id="privesc-read-only" type="checkbox" checked={readOnly} onChange={(e) => setReadOnly(e.target.checked)} />
                  {t('privesc.readOnly')}
                </label>
              </div>
              <div className="field">
                <label htmlFor="privesc-skip-enum" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input id="privesc-skip-enum" type="checkbox" checked={skipEnum} onChange={(e) => setSkipEnum(e.target.checked)} />
                  {t('privesc.skipEnum')}
                </label>
              </div>
              <div className="field">
                <label htmlFor="privesc-refresh" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input id="privesc-refresh" type="checkbox" checked={refreshTool} onChange={(e) => setRefreshTool(e.target.checked)} />
                  {t('privesc.refreshTool')}
                </label>
              </div>
              <div className="field">
                <label htmlFor="privesc-keep" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input id="privesc-keep" type="checkbox" checked={keepTool} onChange={(e) => setKeepTool(e.target.checked)} />
                  {t('privesc.keepTool')}
                </label>
              </div>
              <div className="field">
                <label htmlFor="privesc-dry" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                  <input id="privesc-dry" type="checkbox" checked={dryRun} onChange={(e) => setDryRun(e.target.checked)} />
                  {t('privesc.dryRun')}
                </label>
              </div>
            </div>

            {readOnly && <p className="page-sub" style={{ margin: '8px 0 0' }}>{t('privesc.readOnlyHint')}</p>}
            <p className="page-sub" style={{ margin: '8px 0 0' }}>{t('privesc.toolHint')}</p>

            <div className="toolbar" style={{ marginTop: 10 }}>
              <button type="button" className="btn primary" onClick={run} disabled={running}>
                {running ? t('privesc.running') : t('privesc.run')}
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
              {result?.platform && <span className="badge gray">{t('privesc.platform')}: {result.platform}</span>}
              {result?.read_only && <span className="badge yellow">{t('privesc.readOnly')}</span>}
              {shown.status && <span className="badge gray">{shown.status}</span>}
              {result?.stopped && <span className="badge yellow">{t('ai.stopped')}: {result.stopped}</span>}
            </>
          }
        />
      )}

      {result && <PrivescOutcome result={result} />}
    </>
  )
}

/** The verdict, the evidence behind it, and what was left on the target. */
function PrivescOutcome({ result }: { result: AIPrivescResult }) {
  const { t } = useTranslation()
  const tool = result.tool || {}

  return (
    <div className="card" style={{ marginTop: 14 }}>
      <div className="card-title" style={{ fontSize: 13 }}>{t('privesc.outcome')}</div>

      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 10 }}>
        {result.dry_run ? (
          <span className="badge yellow">{t('privesc.dryRun')}</span>
        ) : result.escalated ? (
          <span className="badge green">
            {result.escalated_via === 'route' ? t('privesc.escalatedRoute') : t('privesc.escalatedSession')}
          </span>
        ) : (
          <span className="badge red">{t('privesc.notEscalated')}</span>
        )}
      </div>

      {result.escalated_via === 'route' && (
        <div className="page-sub" style={{ margin: '0 0 8px' }}>
          {t('privesc.routeNote')}
        </div>
      )}

      {result.evidence && (
        <>
          <div className="page-sub" style={{ margin: '0 0 2px' }}>{t('privesc.evidence')}</div>
          <pre className="viewer-pre mono" style={{ margin: 0, maxHeight: 200, overflow: 'auto' }}>{result.evidence}</pre>
        </>
      )}

      <div className="page-sub" style={{ margin: '10px 0 4px' }}>{t('privesc.toolTitle')}</div>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        {tool.skipped && <span className="badge gray">{t('privesc.toolSkipped')}</span>}
        {tool.asset && <span className="badge gray">{tool.asset}</span>}
        {typeof tool.bytes === 'number' && tool.bytes > 0 && (
          <span className="badge gray">{(tool.bytes / 1024).toFixed(0)} KiB</span>
        )}
        {tool.cached && <span className="badge gray">{t('privesc.toolCached')}</span>}
        {tool.remotePath && <span className="badge gray mono">{tool.remotePath}</span>}
        {tool.removed && <span className="badge green">{t('privesc.toolRemoved')}</span>}
        {tool.error && <span className="badge red">{tool.error}</span>}
      </div>
    </div>
  )
}
