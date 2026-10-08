import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { AIEvent, AICollectStep } from '../../lib/types'

/**
 * The running state of a streamed model run.
 *
 * The server publishes each turn as it happens, so the operator watches the
 * model reason and act instead of a spinner. This is the client-side mirror of
 * that stream: finished steps accumulate, and the reasoning and command for
 * the step in flight are held separately until the step's outcome arrives.
 */
export interface AIStreamState {
  /** The latest human-readable line about the run itself. */
  status: string
  /** The model's reasoning for the step it is about to take. */
  thinking: string
  /** The model's reply text for the turn in flight, as it is written. */
  answer: string
  /** The command the model chose, before its result is known. */
  command: string
  /** Why the model chose that command. */
  reason: string
  /** Steps that have finished, in order. */
  steps: AICollectStep[]
  /** The model's closing explanation, once it stops. */
  summary: string
}

export const emptyAIStream: AIStreamState = {
  status: '',
  thinking: '',
  answer: '',
  command: '',
  reason: '',
  steps: [],
  summary: '',
}

/**
 * Fold one stream event into the running state.
 *
 * A step event closes the step in flight, so the live reasoning and command are
 * cleared with it. That is what makes the "working on it" block disappear
 * exactly when the result appears, rather than leaving a stale command above
 * the output it produced.
 */
export function applyAIEvent(s: AIStreamState, ev: AIEvent): AIStreamState {
  switch (ev.type) {
    case 'status':
      return { ...s, status: ev.text || '' }
    case 'thinking':
      // The finished reasoning replaces whatever the deltas accumulated: the
      // same text, but authoritative.
      return { ...s, thinking: ev.text || '' }
    case 'thinking_delta':
      return { ...s, thinking: s.thinking + (ev.text || '') }
    case 'answer_delta':
      return { ...s, answer: s.answer + (ev.text || '') }
    case 'command':
      return {
        ...s,
        command: ev.step?.command || ev.text || '',
        reason: ev.step?.reason || '',
        thinking: ev.step?.thinking || s.thinking,
        answer: '',
      }
    case 'summary':
      return { ...s, summary: ev.text || '', thinking: '', answer: '', command: '', reason: '' }
    case 'step':
      return ev.step
        ? { ...s, steps: [...s.steps, ev.step], thinking: '', answer: '', command: '', reason: '' }
        : s
    default:
      return s
  }
}

/**
 * Renders one step: the model's reasoning, the command, and what came back.
 *
 * Shared by the collection tab and the escalation tab so both show the same
 * thing in the same shape -- a run is a run, whichever feature started it.
 */
export function AIStep({ step }: { step: AICollectStep }) {
  const { t } = useTranslation()
  return (
    <div style={{ borderTop: '1px solid var(--border, #2a2f36)', paddingTop: 8, marginTop: 8 }}>
      {step.thinking && (
        <>
          <div className="page-sub" style={{ margin: '0 0 2px' }}>{t('ai.thinking')}</div>
          <pre className="viewer-pre mono" style={{ margin: 0, maxHeight: 200, overflow: 'auto', whiteSpace: 'pre-wrap', opacity: 0.85 }}>{step.thinking}</pre>
        </>
      )}
      <div className="page-sub" style={{ margin: '6px 0 2px' }}>{t('ai.command')}</div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <code className="mono">{step.command}</code>
        {step.refused && <span className="badge red">{t('ai.refused')}</span>}
        {typeof step.status === 'number' && step.status > 0 && <span className="badge gray">{step.status}</span>}
      </div>
      {step.reason && !step.thinking && <div className="page-sub" style={{ margin: '4px 0 0' }}>{step.reason}</div>}
      {step.refusal && <div className="alert error" style={{ marginTop: 6 }}>{step.refusal}</div>}
      {step.error && <div className="alert error" style={{ marginTop: 6 }}>{step.error}</div>}
      {step.output && (
        <pre className="viewer-pre mono" style={{ marginTop: 6, maxHeight: 280, overflow: 'auto' }}>{step.output}</pre>
      )}
    </div>
  )
}

/**
 * The live panel for the step in flight.
 *
 * It is shown while the model is reasoning or its command is running, so the
 * operator can tell a slow command from a stuck run.
 */
function AILiveStep({ thinking, answer, command, reason }: { thinking?: string; answer?: string; command?: string; reason?: string }) {
  const { t } = useTranslation()
  if (!thinking && !answer && !command) return null
  return (
    <div style={{ borderTop: '1px solid var(--border, #2a2f36)', paddingTop: 8, marginTop: 8 }}>
      {thinking && (
        <>
          <div className="page-sub" style={{ margin: '0 0 2px' }}>{t('ai.thinking')}</div>
          <pre className="viewer-pre mono" style={{ margin: 0, maxHeight: 200, overflow: 'auto', whiteSpace: 'pre-wrap', opacity: 0.85 }}>{thinking}</pre>
        </>
      )}
      {answer && (
        <>
          <div className="page-sub" style={{ margin: '6px 0 2px' }}>{t('ai.writing')}</div>
          <pre className="viewer-pre mono" style={{ margin: 0, maxHeight: 160, overflow: 'auto', whiteSpace: 'pre-wrap', opacity: 0.85 }}>{answer}</pre>
        </>
      )}
      {command && (
        <>
          <div className="page-sub" style={{ margin: '6px 0 2px' }}>{t('ai.command')}</div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <code className="mono">{command}</code>
            <span className="badge yellow">{t('ai.running')}</span>
          </div>
          {reason && <div className="page-sub" style={{ margin: '4px 0 0' }}>{reason}</div>}
        </>
      )}
    </div>
  )
}

/**
 * The whole step list for a run, live or finished.
 *
 * While the run is streaming the list grows in place and the step in flight is
 * shown below it; when the run ends the last live block is replaced by the
 * step that closed it, so nothing is shown twice.
 */
export default function AIStepsView({
  steps,
  live,
  running,
  summary,
  badges,
}: {
  steps: AICollectStep[]
  live?: { thinking?: string; answer?: string; command?: string; reason?: string }
  running?: boolean
  summary?: string
  badges?: ReactNode
}) {
  const { t } = useTranslation()
  const list = steps || []
  const showLive = !!running && !!live && (!!live.thinking || !!live.answer || !!live.command)

  return (
    <div className="card" style={{ marginTop: 14 }}>
      <div className="card-title">{t('ai.steps')}</div>

      {badges && (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 10 }}>{badges}</div>
      )}

      {summary && <div className="alert" style={{ marginBottom: 10 }}>{summary}</div>}

      {list.length === 0 && !showLive && <p className="page-sub">{t('ai.noSteps')}</p>}

      {list.map((s, i) => <AIStep key={i} step={s} />)}
      {showLive && (
        <AILiveStep thinking={live!.thinking} answer={live!.answer} command={live!.command} reason={live!.reason} />
      )}
    </div>
  )
}
