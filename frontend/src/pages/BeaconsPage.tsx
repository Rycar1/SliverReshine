import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Beacon } from '../lib/types'
import BeaconTasksModal from '../components/BeaconTasksModal'
import InlineEdit from '../components/InlineEdit'
import ConfirmDialog from '../components/common/ConfirmDialog'
import { useToast } from '../components/common/Toast'
import './pages.css'

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

export default function BeaconsPage() {
  const [beacons, setBeacons] = useState<Beacon[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [tasksFor, setTasksFor] = useState<Beacon | null>(null)
  const [removing, setRemoving] = useState<Beacon | null>(null)
  const [busy, setBusy] = useState(false)
  const [monitoring, setMonitoring] = useState(false)
  const [monitorBusy, setMonitorBusy] = useState(false)
  const [pruning, setPruning] = useState(false)
  const [pruneDays, setPruneDays] = useState(7)
  const { t } = useTranslation()
  const navigate = useNavigate()
  const toast = useToast()

  const toggleMonitor = async () => {
    setMonitorBusy(true)
    try {
      if (monitoring) {
        await api.monitorStop()
        setMonitoring(false)
      } else {
        await api.monitorStart()
        setMonitoring(true)
      }
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setMonitorBusy(false)
    }
  }

  const load = async () => {
    try {
      const data = await api.beacons()
      setBeacons(data.beacons || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [])

  const rename = async (b: Beacon, name: string) => {
    try {
      await api.renameBeacon(b.ID, name)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const remove = async (b: Beacon) => {
    setBusy(true)
    try {
      await api.rmBeacon(b.ID)
      setRemoving(null)
      toast.push('success', t('beacons.removed', { name: b.Name }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const prune = async () => {
    setPruning(true)
    try {
      const res = await api.pruneBeacons(pruneDays)
      toast.push('success', t('beacons.pruned', { count: res.pruned }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setPruning(false)
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('beacons.title')}</div>
          <div className="page-sub">{t('beacons.sub', { count: beacons.length })}</div>
        </div>
        <div className="toolbar">
          <label className="prune-control mono">
            {t('beacons.pruneLabel')}
            <input
              type="number"
              min={1}
              value={pruneDays}
              onChange={(e) => setPruneDays(Number(e.target.value) || 1)}
              style={{ width: 56 }}
            />
          </label>
          <button type="button" className="btn" onClick={prune} disabled={pruning}>
            {pruning ? t('common.loading') : t('beacons.prune')}
          </button>
          <button type="button" className={monitoring ? 'btn active' : 'btn'} onClick={toggleMonitor} disabled={monitorBusy}>
            {monitorBusy ? t('common.loading') : monitoring ? t('beacons.monitorOn') : t('beacons.monitorOff')}
          </button>
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {loading ? (
        <div className="empty">{t('common.loading')}</div>
      ) : beacons.length === 0 ? (
        <div className="empty">{t('beacons.empty')}</div>
      ) : (
        <div className="card card-flush">
          <table className="data">
            <thead>
              <tr>
                <th>{t('beacons.thName')}</th>
                <th>{t('beacons.thHost')}</th>
                <th>{t('beacons.thUser')}</th>
                <th>{t('beacons.thOsArch')}</th>
                <th>{t('beacons.thTransport')}</th>
                <th>{t('beacons.thRemote')}</th>
                <th>{t('beacons.thLastCheckin')}</th>
                <th>{t('beacons.thNextCheckin')}</th>
                <th>{t('beacons.thInterval')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {beacons.map((b) => (
                <tr key={b.ID}>
                  <td className="mono">
                    <InlineEdit value={b.Name} onSave={(name) => rename(b, name)} mono />
                    <button type="button" className="link-btn" onClick={() => navigate(`/beacons/${b.ID}`)}>
                      {t('beacons.open')}
                    </button>
                  </td>
                  <td>{b.Hostname}</td>
                  <td>{b.Username}</td>
                  <td>
                    <span className="badge blue">
                      {b.OS}/{b.Arch}
                    </span>
                  </td>
                  <td>{b.Transport}</td>
                  <td className="mono">{b.RemoteAddress}</td>
                  <td>{fmtTime(b.LastCheckin, t)}</td>
                  <td>{fmtTime(b.NextCheckin, t)}</td>
                  <td className="mono">
                    {b.Interval}s / {b.Jitter}%
                  </td>
                  <td>
                    <div className="fs-actions">
                      <button type="button" className="btn sm" onClick={() => setTasksFor(b)}>
                        {t('beacons.tasks')}
                      </button>
                      <button type="button" className="btn sm danger" onClick={() => setRemoving(b)}>
                        {t('beacons.remove')}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {tasksFor && (
        <BeaconTasksModal
          beaconId={tasksFor.ID}
          beaconName={tasksFor.Name}
          onClose={() => setTasksFor(null)}
        />
      )}
      <ConfirmDialog
        open={!!removing}
        title={t('beacons.confirmRemove')}
        danger
        busy={busy}
        confirmLabel={t('beacons.remove')}
        onConfirm={() => removing && remove(removing)}
        onCancel={() => setRemoving(null)}
      >
        <p>{removing ? t('beacons.confirmRemoveBody', { name: removing.Name }) : ''}</p>
      </ConfirmDialog>
    </div>
  )
}
