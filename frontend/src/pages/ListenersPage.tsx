import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import OneLinerPanel from '../components/common/OneLinerPanel'
import OneLinerDialog from '../components/common/OneLinerDialog'
import type { BindListener, Job } from '../lib/types'
import ConfirmDialog from '../components/common/ConfirmDialog'
import ContextMenu from '../components/common/ContextMenu'
import { useToast } from '../components/common/Toast'
import './pages.css'

export default function ListenersPage() {
  const [jobs, setJobs] = useState<Job[]>([])
  const [error, setError] = useState('')
  const [type, setType] = useState('mtls')
  const [addr, setAddr] = useState('0.0.0.0')
  const [port, setPort] = useState('8888')
  const [tls, setTls] = useState(false)
  const [starting, setStarting] = useState(false)
  const [stopping, setStopping] = useState<Job | null>(null)
  const [busy, setBusy] = useState(false)
  const [menu, setMenu] = useState<{ x: number; y: number; job: Job } | null>(null)
  // The listener whose staging commands are being shown. The row is the
  // selection, so this is a Job rather than an id.
  const [commandFor, setCommandFor] = useState<Job | null>(null)
  // Forward (bind) listeners. They are tracked separately from `jobs` because
  // they are the opposite direction: the server dials out, so there is no local
  // port and no protocol to choose — only a target to reach.
  const [binds, setBinds] = useState<BindListener[]>([])
  const [bindHost, setBindHost] = useState('')
  const [bindPort, setBindPort] = useState('')
  const [bindStarting, setBindStarting] = useState(false)
  const { t } = useTranslation()
  const toast = useToast()

  // The two collections are fetched independently rather than with Promise.all.
  // A failed forward-listener query must not blank the reverse-listener table —
  // they are different RPCs against the same server, and one of them failing
  // says nothing about the other. Promise.all would also leave the error banner
  // up and both tables empty on a single transient failure, which is the
  // failure mode that makes an operator think they have no listeners at all.
  const load = async () => {
    const [jobsRes, bindsRes] = await Promise.allSettled([api.jobs(), api.bindListeners()])

    if (jobsRes.status === 'fulfilled') {
      setJobs(jobsRes.value.jobs || [])
    }
    if (bindsRes.status === 'fulfilled') {
      setBinds(bindsRes.value.listeners || [])
    }

    // Only surface an error when nothing could be loaded. A partial failure
    // keeps the stale rows on screen and says so, rather than replacing a
    // populated table with a banner.
    const failure =
      jobsRes.status === 'rejected'
        ? jobsRes.reason
        : bindsRes.status === 'rejected'
          ? bindsRes.reason
          : null
    const nothingLoaded =
      jobsRes.status === 'rejected' && bindsRes.status === 'rejected'
    setError(nothingLoaded && failure ? (failure as Error).message : '')
  }

  // canStage decides whether the staging button is offered. The server ships the
  // authoritative flag on every job (JobView.CanStage); the local name check is
  // only a fallback for an older server that does not send it yet, so the two
  // cannot disagree about a listener the operator is looking at.
  const canStage = (j: Job) => {
    if (typeof j.CanStage === 'boolean') return j.CanStage
    const name = (j.Name || '').toLowerCase()
    return name === 'http' || name === 'https'
  }

  useEffect(() => {
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [])

  const start = async () => {
    setStarting(true)
    try {
      await api.startListener({ type, addr, port: Number(port), tls })
      toast.push('success', t('listeners.started'))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setStarting(false)
    }
  }

  const stop = async (j: Job) => {
    setBusy(true)
    try {
      await api.stopListener(j.ID)
      toast.push('success', t('listeners.stopped', { id: j.ID }))
      setStopping(null)
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  // startBind starts a forward listener. A success here means the dialer is
  // running, not that a session exists: the implant may not have reached its
  // listen path yet and the server keeps retrying. The session appears in the
  // session list when the dial lands, so the toast says exactly that rather
  // than implying the target is already connected.
  const startBind = async () => {
    const portNum = Number(bindPort)
    if (!bindHost.trim()) {
      toast.push('error', t('listeners.bindNeedsHost'))
      return
    }
    if (!portNum || portNum < 1 || portNum > 65535) {
      toast.push('error', t('listeners.bindNeedsPort'))
      return
    }
    setBindStarting(true)
    try {
      await api.dialBind(bindHost.trim(), portNum)
      toast.push('success', t('listeners.bindStarted', { target: `${bindHost.trim()}:${portNum}` }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBindStarting(false)
    }
  }

  const stopBind = async (b: BindListener) => {
    try {
      await api.stopBindListener(b.jobId)
      toast.push('success', t('listeners.bindStopped', { id: b.jobId }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('listeners.title')}</div>
          <div className="page-sub">{t('listeners.sub', { count: jobs.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}

      <div className="card">
        <div className="card-title">{t('listeners.startTitle')}</div>
        <div className="form-grid">
          <div className="field">
            <label>{t('listeners.protocol')}</label>
            <select value={type} onChange={(e) => setType(e.target.value)}>
              <option value="mtls">mTLS</option>
              <option value="http">HTTP</option>
              <option value="https">HTTPS</option>
              <option value="dns">DNS</option>
              <option value="wireguard">WireGuard</option>
            </select>
          </div>
          <div className="field">
            <label>{t('listeners.address')}</label>
            <input value={addr} onChange={(e) => setAddr(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('listeners.port')}</label>
            <input value={port} onChange={(e) => setPort(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
              {t('listeners.enableTls')}
            </label>
            <button type="button" className="btn primary" onClick={start} disabled={starting}>
              {starting ? t('listeners.starting') : t('listeners.start')}
            </button>
          </div>
        </div>
      </div>

      {/* One-liner delivery sits directly under the listener form because that
          is the order it is used in: start a listener, then ask for a command.
          It is a separate card rather than part of the form because it has its
          own inputs and its own result. */}
      <OneLinerPanel />

      {/* Forward (bind) listeners. Deliberately its own card rather than one
          more option in the protocol dropdown above: this is the opposite
          direction. Nothing binds locally, the server dials out, and the
          target is a host the implant is listening on. The hint says so
          because "no sessions yet" means something completely different here
          — the dial has not landed, not that the implant is silent. */}
      <div className="card">
        <div className="card-title">{t('listeners.bindTitle')}</div>
        <div className="page-sub" style={{ marginBottom: 12 }}>
          {t('listeners.bindHint')}
        </div>
        <div className="form-grid">
          <div className="field">
            <label>{t('listeners.bindHost')}</label>
            <input
              value={bindHost}
              placeholder="10.0.0.5"
              onChange={(e) => setBindHost(e.target.value)}
            />
          </div>
          <div className="field">
            <label>{t('listeners.bindPort')}</label>
            <input
              value={bindPort}
              placeholder="4444"
              onChange={(e) => setBindPort(e.target.value)}
            />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button
              type="button"
              className="btn primary"
              onClick={startBind}
              disabled={bindStarting}
            >
              {bindStarting ? t('listeners.bindStarting') : t('listeners.bindStart')}
            </button>
          </div>
        </div>

        {binds.length > 0 && (
          <table className="data" style={{ marginTop: 12 }}>
            <thead>
              <tr>
                <th>{t('listeners.thId')}</th>
                <th>{t('listeners.bindThTarget')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {binds.map((b) => (
                <tr key={b.jobId}>
                  <td className="mono">{b.jobId}</td>
                  <td className="mono">{b.address}</td>
                  <td>
                    <button type="button" className="btn sm danger" onClick={() => stopBind(b)}>
                      {t('listeners.stop')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card card-flush">
        <table className="data">
          <thead>
            <tr>
              <th>{t('listeners.thId')}</th>
              <th>{t('listeners.thName')}</th>
              <th>{t('listeners.thProtocol')}</th>
              <th>{t('listeners.thPort')}</th>
              <th>{t('listeners.thDomains')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {jobs.length === 0 && (
              <tr>
                <td colSpan={6} className="empty">
                  {t('listeners.empty')}
                </td>
              </tr>
            )}
            {jobs.map((j) => (
              <tr
                key={j.ID}
                onContextMenu={(e) => {
                  e.preventDefault()
                  setMenu({ x: e.clientX, y: e.clientY, job: j })
                }}
              >
                <td className="mono">{j.ID}</td>
                <td className="mono">{j.Name}</td>
                <td>
                  <span className="badge blue">{j.Protocol}</span>
                </td>
                <td className="mono">{j.Port}</td>
                <td className="mono">{j.Domains?.join(', ') || '-'}</td>
				<td>
					{/* Only the HTTP family can serve a stage, so the button is offered for
					    those and disabled for the rest rather than failing after a click.
					    The reason is printed inline as well as in the tooltip: a greyed
					    button whose only explanation needs a hover reads as broken. */}
					<button
						type="button"
						className="btn sm"
						disabled={!canStage(j)}
						title={canStage(j) ? t('oneliner.rowButtonHint') : t('oneliner.notStageable', { name: j.Name })}
						onClick={() => setCommandFor(j)}
					>
						{t('oneliner.rowButton')}
					</button>{' '}
					{!canStage(j) && (
						<span className="page-sub" style={{ marginRight: 6 }}>
							{t('listeners.stageNeedsHttp')}
						</span>
					)}
					<button type="button" className="btn sm danger" onClick={() => setStopping(j)}>
						{t('listeners.stop')}
					</button>
				</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
			items={[
				{
					label: t('oneliner.rowButton'),
					// Same gate as the row button, and the same visible reason, so the
					// context menu cannot offer a command the server would refuse.
					disabled: !canStage(menu.job),
					hint: canStage(menu.job) ? undefined : t('listeners.stageNeedsHttp'),
					onSelect: () => setCommandFor(menu.job),
				},
				{
					label: t('listeners.stop'),
					danger: true,
					onSelect: () => setStopping(menu.job),
				},
			]}
        />
      )}
      <ConfirmDialog
        open={!!stopping}
        title={t('jobs.confirmStop')}
        danger
        busy={busy}
        confirmLabel={t('listeners.stop')}
        onConfirm={() => stopping && stop(stopping)}
        onCancel={() => setStopping(null)}
      >
        <p>
          {stopping
            ? t('jobs.confirmStopBody', { name: stopping.Name || stopping.Protocol, id: stopping.ID })
            : ''}
        </p>
		</ConfirmDialog>

		{/* Mounted once and driven by which row was clicked, rather than one
		    dialog per row: the table re-renders every 3 seconds, and a dialog per
		    row would rebuild all of them on each poll. */}
		<OneLinerDialog job={commandFor} onClose={() => setCommandFor(null)} />
		</div>
  )
}
