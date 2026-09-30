import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Event } from '../lib/types'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge, { type StatusTone } from '../components/common/StatusBadge'
import './pages.css'

function badgeTone(type: string): StatusTone {
  if (type.includes('SessionOpened') || type.includes('Started')) return 'green'
  if (type.includes('SessionClosed') || type.includes('Stopped') || type.includes('Error')) return 'red'
  if (type.includes('Listener') || type.includes('Job')) return 'blue'
  return 'yellow'
}

function describe(e: Event): string {
  if (e.Session) return `${e.Session.Name} @ ${e.Session.Hostname} (${e.Session.RemoteAddress})`
  if (e.Beacon) return `${e.Beacon.Name} @ ${e.Beacon.Hostname} (${e.Beacon.RemoteAddress})`
  if (e.Job) return `${e.Job.Name} (${e.Job.Protocol}:${e.Job.Port})`
  if (e.Err) return e.Err
  return JSON.stringify(e.Data || {}).slice(0, 160)
}

export default function EventsPage() {
  const [events, setEvents] = useState<Event[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState<'all' | 'session' | 'beacon' | 'listener'>('all')
  const { t } = useTranslation()

  const load = async () => {
    try {
      const data = await api.events()
      setEvents(data.events || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [])

  const filtered = useMemo(() => {
    if (filter === 'all') return events
    if (filter === 'session') return events.filter((e) => !!e.Session)
    if (filter === 'beacon') return events.filter((e) => !!e.Beacon)
    return events.filter((e) => !!e.Job)
  }, [events, filter])

  const columns: Column<Event>[] = [
    {
      key: 'Type',
      label: t('events.thType'),
      sortable: true,
      render: (e) => <StatusBadge tone={badgeTone(e.Type)}>{e.Type}</StatusBadge>,
    },
    { key: 'Details', label: t('events.thDetails'), render: (e) => <span className="mono">{describe(e)}</span> },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('events.title')}</div>
          <div className="page-sub">{t('events.sub')}</div>
        </div>
        <div className="toolbar">
          <div className="seg">
            {(['all', 'session', 'beacon', 'listener'] as const).map((f) => (
              <button type="button" key={f} className={filter === f ? 'active' : ''} onClick={() => setFilter(f)}>
                {f === 'all'
                  ? t('events.filterAll')
                  : f === 'session'
                    ? t('events.filterSessions')
                    : f === 'beacon'
                      ? t('events.filterBeacons')
                      : t('events.filterListeners')}
              </button>
            ))}
          </div>
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div className="card card-flush">
        <DataTable
          columns={columns}
          rows={filtered}
          rowKey={(e, i) => `${i}-${e.Type}`}
          loading={loading}
          empty={t('events.empty')}
          defaultSort={{ key: 'Type', dir: 'asc' }}
        />
      </div>
    </div>
  )
}
