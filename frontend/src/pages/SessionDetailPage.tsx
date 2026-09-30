import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Session } from '../lib/types'
import InlineEdit from '../components/InlineEdit'
import ConfirmDialog from '../components/common/ConfirmDialog'
import { useToast } from '../components/common/Toast'
import FilesTab from '../components/session/FilesTab'
import ProcessesTab from '../components/session/ProcessesTab'
import NetworkTab from '../components/session/NetworkTab'
import EnvTab from '../components/session/EnvTab'
import ExecTab from '../components/session/ExecTab'
import ScreenshotTab from '../components/session/ScreenshotTab'
import PortfwdTab from '../components/session/PortfwdTab'
import RegistryTab from '../components/session/RegistryTab'
import AdvancedTab from '../components/session/AdvancedTab'
import TokensTab from '../components/session/TokensTab'
import WgTab from '../components/session/WgTab'
import PivotTab from '../components/session/PivotTab'
import ServicesTab from '../components/session/ServicesTab'
import PostExTab from '../components/session/PostExTab'
import PersistenceTab from '../components/session/PersistenceTab'
import HarvestTab from '../components/session/HarvestTab'
import './pages.css'
import './session.css'

const TABS = ['files', 'processes', 'network', 'env', 'exec', 'screenshot', 'portfwd', 'registry', 'advanced', 'tokens', 'wg', 'pivot', 'services', 'postex', 'persistence', 'harvest'] as const
type TabKey = (typeof TABS)[number]

type TFunc = ReturnType<typeof useTranslation>['t']

function fmtTime(ts: string, t: TFunc): string {
  if (!ts) return '-'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const now = Date.now()
  const diff = Math.round((now - d.getTime()) / 1000)
  if (diff < 60) return t('time.secondsAgo', { count: diff })
  if (diff < 3600) return t('time.minutesAgo', { count: Math.round(diff / 60) })
  if (diff < 86400) return t('time.hoursAgo', { count: Math.round(diff / 3600) })
  return t('time.daysAgo', { count: Math.round(diff / 86400) })
}

export default function SessionDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const toast = useToast()
  const [session, setSession] = useState<Session | null>(null)
  const [error, setError] = useState('')
  const [tab, setTab] = useState<TabKey>('files')
  const [pingMsg, setPingMsg] = useState('')
  const [killing, setKilling] = useState(false)
  const [closing, setClosing] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let alive = true
    const poll = () => {
      api
        .sessions()
        .then((d) => {
          if (!alive) return
          const s = d.sessions.find((x) => x.ID === id)
          setSession(s || null)
          if (!s) setError(t('detail.notFound'))
        })
        .catch((e) => {
          if (alive) setError((e as Error).message)
        })
    }
    poll()
    const timer = setInterval(poll, 5000)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [id, t])

  const rename = async (name: string) => {
    if (!session) return
    try {
      await api.renameSession(session.ID, name)
      setSession({ ...session, Name: name })
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const kill = async () => {
    if (!session) return
    setBusy(true)
    try {
      await api.killSession(session.ID)
      toast.push('success', t('sessions.killed', { name: session.Name }))
      navigate('/')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
      setKilling(false)
    }
  }

  const ping = async () => {
    setPingMsg('')
    try {
      const r = await api.ping(session!.ID)
      setPingMsg(t('detail.pingOk', { nonce: r.nonce }))
    } catch (e) {
      setPingMsg(`${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  const closeSession = async () => {
    if (!session) return
    setBusy(true)
    try {
      await api.closeSession(session.ID)
      toast.push('success', t('sessions.closed', { name: session.Name }))
      navigate('/')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
      setClosing(false)
    }
  }

  return (
    <div className="page page-detail">
      {error && <div className="error-banner">{error}</div>}
      {pingMsg && (
        <div className="success-banner">
          {pingMsg}
        </div>
      )}

      {session && (
        <div className="issue-layout">
          <div className="issue-main">
            <div className="issue-header">
              <div className="issue-title-block">
                <div className="issue-title-row">
                  <InlineEdit value={session.Name} onSave={rename} />
                  <span className={`badge ${session.IsDead ? 'red' : 'green'}`}>
                    {session.IsDead ? t('detail.sidebar.dead') : t('detail.sidebar.active')}
                  </span>
                </div>
                <div className="page-sub">
                  {session.Hostname} · {session.RemoteAddress} · {session.Transport}
                </div>
              </div>
              <div className="toolbar">
                <button type="button" className="btn primary" onClick={() => navigate(`/sessions/${session.ID}/terminal`)}>
                  {t('detail.openTerminal')}
                </button>
                <button type="button" className="btn" onClick={() => setTab('processes')}>
                  {t('detail.tabs.processes')}
                </button>
                <button type="button" className="btn" onClick={() => setTab('network')}>
                  {t('detail.tabs.network')}
                </button>
                <button type="button" className="btn" onClick={() => setTab('screenshot')}>
                  {t('detail.tabs.screenshot')}
                </button>
                <button type="button" className="btn" onClick={ping} title="Ping">
                  {t('detail.ping')}
                </button>
                <button type="button" className="btn" onClick={() => navigate('/')}>
                  {t('detail.back')}
                </button>
              </div>
            </div>

            <div className="tabs">
              {TABS.map((key) => (
                <button type="button"
                  key={key}
                  className={`tab ${tab === key ? 'active' : ''}`}
                  onClick={() => setTab(key)}
                >
                  {t(`detail.tabs.${key}`)}
                </button>
              ))}
            </div>

            <div className="tab-content">
              {tab === 'files' && <FilesTab sessionId={session.ID} os={session.OS} />}
              {tab === 'processes' && <ProcessesTab sessionId={session.ID} />}
              {tab === 'network' && <NetworkTab sessionId={session.ID} />}
              {tab === 'env' && <EnvTab sessionId={session.ID} />}
              {tab === 'exec' && <ExecTab sessionId={session.ID} />}
              {tab === 'screenshot' && <ScreenshotTab sessionId={session.ID} />}
              {tab === 'portfwd' && <PortfwdTab sessionId={session.ID} />}
              {tab === 'registry' && <RegistryTab sessionId={session.ID} os={session.OS} />}
              {tab === 'advanced' && <AdvancedTab sessionId={session.ID} />}
              {tab === 'tokens' && <TokensTab sessionId={session.ID} os={session.OS} />}
              {tab === 'wg' && <WgTab sessionId={session.ID} isWg={session.Transport === 'wg'} />}
              {tab === 'pivot' && <PivotTab sessionId={session.ID} />}
              {tab === 'services' && <ServicesTab sessionId={session.ID} os={session.OS} />}
              {tab === 'postex' && <PostExTab sessionId={session.ID} />}
              {tab === 'persistence' && <PersistenceTab sessionId={session.ID} os={session.OS} />}
              {tab === 'harvest' && <HarvestTab sessionId={session.ID} />}
            </div>
          </div>

          <aside className="issue-side">
            <div className="side-card">
              <div className="side-card-title">{t('detail.sidebar.properties')}</div>
              <div className="side-row">
                <span className="side-label">ID</span>
                <span className="side-value mono">{session.ID}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.hostname')}</span>
                <span className="side-value">{session.Hostname}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.user')}</span>
                <span className="side-value">{session.Username}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.os')}</span>
                <span className="side-value">
                  {session.OS}/{session.Arch}
                </span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.pid')}</span>
                <span className="side-value mono">{session.PID}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.transport')}</span>
                <span className="side-value">{session.Transport}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.remote')}</span>
                <span className="side-value mono">{session.RemoteAddress}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.c2')}</span>
                <span className="side-value mono">{session.ActiveC2}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.checkin')}</span>
                <span className="side-value">{fmtTime(session.LastCheckin, t)}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.locale')}</span>
                <span className="side-value">{session.Locale || '-'}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.agent')}</span>
                <span className="side-value mono">v{session.AgentVersion}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('detail.sidebar.uid')}</span>
                <span className="side-value mono">{session.UID}/{session.GID}</span>
              </div>
            </div>

            <div className="side-actions">
              <button type="button" className="btn" onClick={() => setClosing(true)}>
                {t('detail.sidebar.close')}
              </button>
              <button type="button" className="btn danger" onClick={() => setKilling(true)}>
                {t('detail.sidebar.kill')}
              </button>
            </div>
          </aside>
        </div>
      )}

      {!error && !session && <div className="empty">{t('common.loading')}</div>}

      <ConfirmDialog
        open={closing}
        title={t('sessions.confirmClose')}
        busy={busy}
        confirmLabel={t('detail.sidebar.close')}
        onConfirm={closeSession}
        onCancel={() => setClosing(false)}
      >
        <p>{session ? t('sessions.confirmCloseBody', { name: session.Name }) : ''}</p>
      </ConfirmDialog>

      <ConfirmDialog
        open={killing}
        title={t('sessions.confirmKill')}
        danger
        busy={busy}
        confirmLabel={t('sessions.kill')}
        onConfirm={kill}
        onCancel={() => setKilling(false)}
      >
        <p>{session ? t('sessions.confirmKillBody', { name: session.Name, id: session.ID }) : ''}</p>
      </ConfirmDialog>
    </div>
  )
}
