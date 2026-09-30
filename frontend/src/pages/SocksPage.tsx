import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { SocksProxy, Session } from '../lib/types'
import './pages.css'

export default function SocksPage() {
  const { t } = useTranslation()
  const [proxies, setProxies] = useState<SocksProxy[]>([])
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [starting, setStarting] = useState(false)

  const [sessionId, setSessionId] = useState('')
  const [bindAddr, setBindAddr] = useState('127.0.0.1')
  const [bindPort, setBindPort] = useState('1080')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  const load = useCallback(async () => {
    try {
      const d = await api.socksList()
      setProxies(d.proxies || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  const loadSessions = useCallback(async () => {
    try {
      const d = await api.sessions()
      setSessions(d.sessions || [])
      setSessionId((cur) => cur || d.sessions?.[0]?.ID || '')
    } catch {
      /* sessions fetch is best-effort */
    }
  }, [])

  useEffect(() => {
    load()
    loadSessions()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [load, loadSessions])

  const start = async () => {
    setStarting(true)
    setMessage('')
    setError('')
    try {
      const res = await api.socksStart({
        session_id: sessionId,
        bind_addr: bindAddr,
        bind_port: Number(bindPort) || 0,
        username: username || undefined,
        password: password || undefined,
      })
      setMessage(t('socks.started', { addr: res.bindAddr ?? bindAddr, port: res.bindPort ?? bindPort }))
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setStarting(false)
    }
  }

  const stop = async (id: number) => {
    setError('')
    try {
      await api.socksStop(id)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('socks.title')}</div>
          <div className="page-sub">{t('socks.sub', { count: proxies.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {message && (
        <div
          className="error-banner"
          style={{
            borderColor: 'var(--green)',
            color: 'var(--green)',
            background: 'var(--success-bg)',
          }}
        >
          {message}
        </div>
      )}

      <div className="card">
        <div className="card-title">{t('socks.new')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('socks.hint')}
        </p>
        <div className="form-grid">
          <div className="field">
            <label>{t('socks.sessionId')}</label>
            <select value={sessionId} onChange={(e) => setSessionId(e.target.value)}>
              {sessions.length === 0 && <option value="">-</option>}
              {sessions.map((s) => (
                <option key={s.ID} value={s.ID}>
                  {s.Name} ({s.Hostname}) {s.ID.slice(0, 8)}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('socks.bindAddr')}</label>
            <input value={bindAddr} onChange={(e) => setBindAddr(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('socks.bindPort')}</label>
            <input type="number" value={bindPort} onChange={(e) => setBindPort(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('socks.username')}</label>
            <input value={username} onChange={(e) => setUsername(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('socks.password')}</label>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={start} disabled={starting || !sessionId}>
              {starting ? t('socks.starting') : t('socks.start')}
            </button>
          </div>
        </div>
      </div>

      <div className="card card-flush">
        <table className="data">
          <thead>
            <tr>
              <th>{t('socks.thId')}</th>
              <th>{t('socks.thSession')}</th>
              <th>{t('socks.thBind')}</th>
              <th>{t('socks.thAuth')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={5} className="empty">
                  {t('common.loading')}
                </td>
              </tr>
            ) : proxies.length === 0 ? (
              <tr>
                <td colSpan={5} className="empty">
                  {t('socks.empty')}
                </td>
              </tr>
            ) : null}
            {!loading && proxies.map((p) => (
              <tr key={p.ID}>
                <td className="mono">{p.ID}</td>
                <td className="mono">{p.SessionID.slice(0, 12)}</td>
                <td className="mono">{`${p.BindAddr}:${p.BindPort}`}</td>
                <td>{p.Username ? p.Username : '-'}</td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => stop(p.ID)}>
                    {t('socks.stop')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
