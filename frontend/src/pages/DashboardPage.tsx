import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useConnection } from '../lib/connection'
import { BarList, Donut, Legend, CHART_COLORS } from '../components/Charts'
import type { Beacon, Session, Job, Event } from '../lib/types'
import './pages.css'

type TFunc = ReturnType<typeof useTranslation>['t']

function fmtTime(ts: string, t: TFunc): string {
  if (!ts) return '-'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const diff = Math.round((Date.now() - d.getTime()) / 1000)
  if (diff < 0) return t('time.justNow')
  if (diff < 60) return t('time.secondsAgo', { count: diff })
  if (diff < 3600) return t('time.minutesAgo', { count: Math.round(diff / 60) })
  if (diff < 86400) return t('time.hoursAgo', { count: Math.round(diff / 3600) })
  return t('time.daysAgo', { count: Math.round(diff / 86400) })
}

function badgeClass(type: string): string {
  if (type.includes('SessionOpened') || type.includes('Started')) return 'green'
  if (type.includes('SessionClosed') || type.includes('Stopped') || type.includes('Error')) return 'red'
  if (type.includes('Listener') || type.includes('Job')) return 'blue'
  return 'yellow'
}

export default function DashboardPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { connected, counts } = useConnection()
  const [sessions, setSessions] = useState<Session[]>([])
  const [beacons, setBeacons] = useState<Beacon[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const [events, setEvents] = useState<Event[]>([])
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    if (!connected) {
      setSessions([])
      setBeacons([])
      setJobs([])
      setEvents([])
      return
    }
    try {
      const [ss, js, bs] = await Promise.all([api.sessions(), api.jobs(), api.beacons()])
      setSessions(ss.sessions || [])
      setJobs(js.jobs || [])
      setBeacons(bs.beacons || [])
      try {
        const ev = await api.events()
        setEvents((ev.events || []).slice(0, 8))
      } catch {
        // Recent activity is auxiliary; a failure must not break the overview.
      }
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [connected])

  useEffect(() => {
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [load])

  const statCards: { label: string; value: number; to: string }[] = [
    { label: t('dashboard.sessions'), value: counts.sessions, to: '/sessions' },
    { label: t('dashboard.beacons'), value: counts.beacons, to: '/beacons' },
    { label: t('dashboard.listeners'), value: counts.jobs, to: '/listeners' },
    { label: t('dashboard.builders'), value: counts.builders, to: '/implants' },
    { label: t('dashboard.socks'), value: counts.socks, to: '/socks' },
  ]

  const byOs = useMemo(() => {
    const counts = new Map<string, number>()
    for (const s of sessions) {
      const os = (s.OS || '').toLowerCase()
      const key =
        os === 'darwin' ? 'macOS' : os ? os.charAt(0).toUpperCase() + os.slice(1) : 'Other'
      counts.set(key, (counts.get(key) ?? 0) + 1)
    }
    const order = ['Windows', 'Linux', 'macOS']
    const sorted = [...counts.entries()].sort((a, b) => {
      const ia = order.indexOf(a[0])
      const ib = order.indexOf(b[0])
      if (ia !== -1 || ib !== -1) return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib)
      return b[1] - a[1]
    })
    return sorted.map(([label, value], i) => ({
      label,
      value,
      color: CHART_COLORS[i % CHART_COLORS.length],
    }))
  }, [sessions])

  const byTransport = useMemo(() => {
    const counts = new Map<string, number>()
    const bump = (t?: string) => {
      const norm = (t || '').toLowerCase()
      const key =
        norm === 'mtls'
          ? 'mTLS'
          : norm === 'http'
            ? 'HTTP'
            : norm === 'dns'
              ? 'DNS'
              : norm === 'wg' || norm === 'wireguard'
                ? 'WG'
                : norm
                  ? norm.toUpperCase()
                  : 'Other'
      counts.set(key, (counts.get(key) ?? 0) + 1)
    }
    sessions.forEach((s) => bump(s.Transport))
    beacons.forEach((b) => bump(b.Transport))
    const sorted = [...counts.entries()].sort((a, b) => b[1] - a[1])
    return sorted.map(([label, value], i) => ({
      label,
      value,
      color: CHART_COLORS[i % CHART_COLORS.length],
    }))
  }, [sessions, beacons])

  const topHosts = useMemo(() => {
    const counts = new Map<string, number>()
    for (const s of sessions) {
      const host = s.Hostname || 'unknown'
      counts.set(host, (counts.get(host) ?? 0) + 1)
    }
    return [...counts.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, 5)
      .map(([label, value], i) => ({
        label,
        value,
        color: CHART_COLORS[i % CHART_COLORS.length],
      }))
  }, [sessions])

  const byEvent = useMemo(() => {
    const counts = new Map<string, number>()
    for (const e of events) {
      const type = e.Type || 'Unknown'
      counts.set(type, (counts.get(type) ?? 0) + 1)
    }
    return [...counts.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, 5)
      .map(([label, value], i) => ({
        label,
        value,
        color: CHART_COLORS[i % CHART_COLORS.length],
      }))
  }, [events])

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('dashboard.title')}</div>
          <div className="page-sub">
            {connected ? t('dashboard.sub') : t('dashboard.disconnected')}
          </div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}

      <div className="dash-stats">
        {statCards.map((c) => (
          <button type="button" key={c.label} className="dash-stat" onClick={() => navigate(c.to)}>
            <div className="dash-stat-value mono">{c.value}</div>
            <div className="dash-stat-label">{c.label}</div>
          </button>
        ))}
      </div>

      <div className="viz-grid">
        <div className="card viz-card">
          <div className="card-title" style={{ marginBottom: 14 }}>
            {t('dashboard.byOs')}
          </div>
          <div className="viz-body">
            <Donut
              data={byOs}
              center={String(sessions.length)}
              centerSub={t('dashboard.total')}
              emptyText={t('dashboard.noData')}
            />
            <Legend data={byOs} />
          </div>
        </div>
        <div className="card viz-card">
          <div className="card-title" style={{ marginBottom: 14 }}>
            {t('dashboard.byTransport')}
          </div>
          <div className="viz-body">
            <Donut
              data={byTransport}
              center={String(sessions.length + beacons.length)}
              centerSub={t('dashboard.total')}
              emptyText={t('dashboard.noData')}
            />
            <Legend data={byTransport} />
          </div>
        </div>
        <div className="card viz-card">
          <div className="card-title" style={{ marginBottom: 14 }}>
            {t('dashboard.topHosts')}
          </div>
          <BarList rows={topHosts} emptyText={t('dashboard.noData')} />
        </div>
        <div className="card viz-card">
          <div className="card-title" style={{ marginBottom: 14 }}>
            {t('dashboard.activity')}
          </div>
          <BarList rows={byEvent} emptyText={t('dashboard.noData')} />
        </div>
      </div>

      <div className="dash-grid">
        <div className="card card-flush">
          <div className="card-title" style={{ padding: '12px 14px', borderBottom: '1px solid var(--border)', margin: 0 }}>
            {t('dashboard.activeSessions')}
          </div>
          <table className="data">
            <thead>
              <tr>
                <th>{t('sessions.thName')}</th>
                <th>{t('sessions.thHost')}</th>
                <th>{t('sessions.thUser')}</th>
                <th>{t('sessions.thOsArch')}</th>
                <th>{t('sessions.thCheckin')}</th>
              </tr>
            </thead>
            <tbody>
              {sessions.length === 0 && (
                <tr>
                  <td colSpan={5} className="empty">
                    {t('sessions.empty')}
                  </td>
                </tr>
              )}
              {sessions.map((s) => (
                <tr key={s.UUID || s.ID} onClick={() => navigate(`/sessions/${s.ID}`)} style={{ cursor: 'pointer' }}>
                  <td className="mono">{s.Name}</td>
                  <td>{s.Hostname}</td>
                  <td>{s.Username}</td>
                  <td>
                    <span className="badge blue">
                      {s.OS}/{s.Arch}
                    </span>
                  </td>
                  <td>{fmtTime(s.LastCheckin, t)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="card card-flush">
          <div className="card-title" style={{ padding: '12px 14px', borderBottom: '1px solid var(--border)', margin: 0 }}>
            {t('dashboard.listeners')}
          </div>
          <table className="data">
            <thead>
              <tr>
                <th>{t('listeners.thName')}</th>
                <th>{t('listeners.thProtocol')}</th>
                <th>{t('listeners.thPort')}</th>
              </tr>
            </thead>
            <tbody>
              {jobs.length === 0 && (
                <tr>
                  <td colSpan={3} className="empty">
                    {t('listeners.empty')}
                  </td>
                </tr>
              )}
              {jobs.map((j) => (
                <tr key={j.ID}>
                  <td className="mono">{j.Name || '-'}</td>
                  <td>
                    <span className="badge blue">{j.Protocol}</span>
                  </td>
                  <td className="mono">{j.Port}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="card card-flush">
        <div className="card-title" style={{ padding: '12px 14px', borderBottom: '1px solid var(--border)', margin: 0 }}>
          {t('dashboard.recentActivity')}
          <button type="button" className="card-link" onClick={() => navigate('/events')}>
            {t('common.viewAll')}
          </button>
        </div>
        <table className="data">
          <thead>
            <tr>
              <th>{t('events.thType')}</th>
              <th>{t('events.thDetails')}</th>
            </tr>
          </thead>
          <tbody>
            {events.length === 0 && (
              <tr>
                <td colSpan={2} className="empty">
                  {t('events.empty')}
                </td>
              </tr>
            )}
            {events.map((e, i) => (
              <tr key={i}>
                <td>
                  <span className={`badge ${badgeClass(e.Type)}`}>{e.Type}</span>
                </td>
                <td className="mono" style={{ fontSize: 12 }}>
                  {e.Session
                    ? `${e.Session.Name} @ ${e.Session.Hostname} (${e.Session.RemoteAddress})`
                    : e.Beacon
                      ? `${e.Beacon.Name} @ ${e.Beacon.Hostname} (${e.Beacon.RemoteAddress})`
                      : e.Job
                        ? `${e.Job.Name} (${e.Job.Protocol}:${e.Job.Port})`
                        : e.Err
                          ? e.Err
                          : JSON.stringify(e.Data || {}).slice(0, 120)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
