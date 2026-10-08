import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { OneLinerResult, OneLinerTarget } from '../../lib/types'
import { classifyOneLinerError, oneLinerErrorText } from './oneLinerError'

/**
 * Reconciles the selected listener against the freshest listener list.
 *
 * Stopping and restarting a listener hands it a new job id, so a selection made
 * before the restart points at a listener that no longer exists. The select
 * renders the live listener anyway -- a value with no matching option falls back
 * to the first option -- so the panel looks correct while carrying the dead id,
 * and the build fails with "no listener with job id N". Keeping the selection
 * only while it is still present is what stops the two from drifting apart.
 *
 * An empty list yields '' rather than keeping a stale id, so a later listener
 * gets picked up instead of reviving the old one.
 */
export function reconcileJobId(current: number | '', targets: OneLinerTarget[]): number | '' {
  const usable = targets.filter((x) => x.can_stage)
  if (current !== '' && usable.some((x) => x.job_id === current)) {
    return current
  }
  return usable.length > 0 ? usable[0].job_id : ''
}

/**
 * Whether the value only routes from the machine it names.
 *
 * Exported for tests. Kept in step with isLoopbackHost on the Go side: the panel
 * must not warn about an address the backend would accept, and must not stay
 * silent about one it would never choose.
 */
export function isLoopbackHost(value: string): boolean {
  const h = value.trim().replace(/^\[|\]$/g, '')
  if (h.toLowerCase() === 'localhost') return true
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h)
  if (v4) return Number(v4[1]) === 127
  return h === '::1'
}

/**
 * One-liner delivery: pick a listener, get a command that gets a session.
 *
 * This is the whole flow in one control because the underlying operations are
 * useless separately. Building an implant, publishing a stage and generating a
 * command are three steps that have to agree on the C2 address, the fetch URL
 * and the port, and an operator assembling them by hand gets a command that
 * fails on the target with nothing on the console explaining why. Here the
 * listener supplies all three, so they cannot drift.
 */
export default function OneLinerPanel() {
  const { t } = useTranslation()
  const [targets, setTargets] = useState<OneLinerTarget[]>([])
  const [jobId, setJobId] = useState<number | ''>('')
  const [platform, setPlatform] = useState('windows')
  const [host, setHost] = useState('')
  // Whether the operator has typed into the host field. Until they do, the field
  // tracks the address the backend would derive, so the panel shows what the
  // implant will actually dial rather than an empty box.
  const [hostTouched, setHostTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<OneLinerResult | null>(null)
  const [copied, setCopied] = useState('')

  const load = useCallback(async () => {
    try {
      const res = await api.oneLinerTargets()
      const all = res.targets || []
      setTargets(all)
      // Preselect the first eligible listener rather than making the operator
      // pick from a list where most entries cannot be used.
      setJobId((cur) => reconcileJobId(cur, all))
    } catch (err) {
      setError((err as Error).message)
    }
  }, [])

  useEffect(() => {
    load()
    const timer = window.setInterval(load, 10000)
    return () => window.clearInterval(timer)
  }, [load])

  const usable = targets.filter((x) => x.can_stage)

  // The address a command built for the selected listener dials when the field
  // is left blank. Resolved by the backend from the listener's own bind address
  // and, failing that, the address this console was reached on.
  const selected = usable.find((x) => x.job_id === jobId)
  const suggestedHost = selected?.callback_host || ''

  // A wildcard here is a mistake, not an instruction, and the backend ignores it
  // and falls back to the listener's recorded address. Saying so before the build
  // is what stops the operator from reading a 0.0.0.0 command and concluding the
  // console derived it.
  const hostIsWildcard = ['0.0.0.0', '::', '[::]', '*'].includes(host.trim())

  // Loopback is the quieter form of the same mistake: 127.0.0.1 is a real
  // address, so it reads like a working callback, but it only routes from this
  // host. It is never derived (the backend skips it), so a value here is one the
  // operator typed -- which is honoured, because a target running on the C2 host
  // is a real case. Warn rather than rewrite.
  const hostIsLoopback = isLoopbackHost(host)

  // Fill the field with the address the backend would derive, so the operator
  // reads the callback the implant will use before a single build runs. An empty
  // field was what made "0.0.0.0" look like a reasonable thing to type -- and a
  // typed wildcard used to be accepted and emitted. Tracking the selection stops
  // the field going stale, and touching it hands control back to the operator.
  useEffect(() => {
    if (hostTouched) return
    setHost(suggestedHost)
  }, [suggestedHost, hostTouched])

  const generate = async () => {
    // Checked against the freshest list rather than trusting the select: the
    // listener can be stopped from another tab, and the row that carried the
    // selected job id then vanishes while the select still shows a live one.
    // Without this the request goes out with a dead id and comes back as
    // "no listener with job id N" -- naming a listener that is nowhere on
    // screen, because the list was refreshed and the selection was not.
    if (jobId === '' || !usable.some((x) => x.job_id === jobId)) {
      setError(t('oneliner.pickListener'))
      // Resync now rather than waiting up to a poll interval, so the message is
      // a one-click annoyance instead of something the operator has to guess at.
      void load()
      return
    }
    // A wildcard is a bind address, not a destination, so it is dropped here
    // where the field is rather than sent for the backend to ignore -- the
    // warning under the field promises exactly this. The backend keeps the same
    // guard, because it is the last line of defence and not every caller goes
    // through this form.
    const typedHost = hostIsWildcard ? '' : host.trim()
    // Nothing to send and nothing to fall back on: the backend would refuse, and
    // its refusal is a sentence about bind addresses rather than an instruction.
    // Stopping here keeps the message in the operator's language and next to the
    // field that fixes it.
    if (typedHost === '' && !suggestedHost) {
      setError(t('oneliner.hostNeeded'))
      return
    }
    setBusy(true)
    setError('')
    setResult(null)
    try {
      const res = await api.oneLiner({
        job_id: Number(jobId),
        platform,
        host: typedHost || undefined,
      })
      setResult(res)
    } catch (err) {
      // A listener stopped between the last poll and this click reaches the
      // backend before the poll notices, so the reply names a job id that is
      // still on screen. Resync now rather than waiting up to a poll interval,
      // and explain the failure in the operator's language instead of surfacing
      // the raw backend sentence.
      const message = (err as Error).message
      setError(oneLinerErrorText(message, t, 'panel'))
      if (classifyOneLinerError(message) === 'pick') {
        void load()
      }
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
      // Clipboard access can be refused; the field is selectable either way.
      setCopied('')
    }
  }

  return (
    <div className="card">
      <div className="card-title">{t('oneliner.title')}</div>
      <div className="page-sub" style={{ marginBottom: 12 }}>
        {t('oneliner.hint')}
      </div>

      {error && <div className="error-banner">{error}</div>}

      {usable.length === 0 ? (
        <div className="page-sub">{t('oneliner.noListeners')}</div>
      ) : (
        <div className="form-grid">
          <div className="field">
            <label>{t('oneliner.listener')}</label>
            <select value={jobId} onChange={(e) => setJobId(Number(e.target.value))}>
              {usable.map((x) => (
                <option key={x.job_id} value={x.job_id}>
                  {x.name} :{x.port}
                  {x.domains && x.domains.filter(Boolean).length > 0
                    ? ` (${x.domains.filter(Boolean).join(', ')})`
                    : ''}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('oneliner.platform')}</label>
            <select value={platform} onChange={(e) => setPlatform(e.target.value)}>
              <option value="windows">Windows</option>
              <option value="linux">Linux</option>
              <option value="darwin">macOS</option>
            </select>
          </div>
          <div className="field">
            <label>{t('oneliner.host')}</label>
            <input
              value={host}
              placeholder={t('oneliner.hostPlaceholder')}
              onChange={(e) => {
                setHost(e.target.value)
                setHostTouched(true)
              }}
            />
            {!hostTouched && suggestedHost && (
              <div className="page-sub" style={{ marginTop: 4 }}>
                {t('oneliner.hostAuto')}
              </div>
            )}
            {!hostTouched && !suggestedHost && (
              <div className="page-sub" style={{ marginTop: 4 }}>
                {t('oneliner.hostNeeded')}
              </div>
            )}
            {hostIsWildcard && (
              <div className="page-sub" style={{ marginTop: 4 }}>
                {t('oneliner.hostIsWildcard')}
              </div>
            )}
            {hostIsLoopback && !hostIsWildcard && (
              <div className="page-sub" style={{ marginTop: 4 }}>
                {t('oneliner.hostIsLoopback')}
              </div>
            )}
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={generate} disabled={busy}>
              {busy ? t('oneliner.building') : t('oneliner.generate')}
            </button>
          </div>
        </div>
      )}

      {result && (
        <div style={{ marginTop: 16 }}>
          <div className="card-title" style={{ fontSize: 13 }}>
            {t('oneliner.command')}
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
            <textarea
              readOnly
              value={result.command}
              rows={3}
              style={{
                flex: 1,
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
            <button
              type="button"
              className="btn"
              onClick={() => copy(result.command, 'main')}
            >
              {copied === 'main' ? t('common.copied') : t('common.copy')}
            </button>
          </div>

          {/* The target, not the command, is what an operator usually needs to
              check first: a command that fetches a URL the target cannot route
              to looks exactly like one that works. */}
          <div className="side-row" style={{ marginTop: 8 }}>
            <span className="side-label">{t('oneliner.fetchUrl')}</span>
            <span className="side-value mono">{result.url}</span>
          </div>
          <div className="side-row">
            <span className="side-label">{t('oneliner.callback')}</span>
            <span className="side-value mono">{result.c2_url}</span>
          </div>
          <div className="side-row">
            <span className="side-label">{t('oneliner.builtAs')}</span>
            <span className="side-value mono">{result.staged_as}</span>
          </div>

          {result.warning && <div className="error-banner">{result.warning}</div>}

          {result.alternatives.length > 0 && (
            <details style={{ marginTop: 12 }}>
              <summary style={{ cursor: 'pointer', fontSize: 12 }}>
                {t('oneliner.alternatives', { count: result.alternatives.length })}
              </summary>
              {result.alternatives.map((a) => (
                <div key={a.delivery} style={{ marginTop: 10 }}>
                  <div className="side-row">
                    <span className="side-label">{a.label || a.delivery}</span>
                    <button
                      type="button"
                      className="btn sm"
                      onClick={() => copy(a.command, a.delivery)}
                    >
                      {copied === a.delivery ? t('common.copied') : t('common.copy')}
                    </button>
                  </div>
                  <textarea
                    readOnly
                    value={a.command}
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
                    }}
                    onFocus={(e) => e.currentTarget.select()}
                  />
                </div>
              ))}
            </details>
          )}
        </div>
      )}
    </div>
  )
}
