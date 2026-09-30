import { useCallback, useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Beacon, BeaconTask } from '../lib/types'
import InlineEdit from '../components/InlineEdit'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import ConfirmDialog from '../components/common/ConfirmDialog'
import DetailDrawer from '../components/common/DetailDrawer'
import { useToast } from '../components/common/Toast'
import './pages.css'
import './session.css'

type TFunc = ReturnType<typeof useTranslation>['t']

function fmtTime(ts: string | number, t: TFunc): string {
  if (!ts) return '-'
  const num = typeof ts === 'number' ? ts * 1000 : new Date(ts).getTime()
  const d = new Date(num)
  if (Number.isNaN(d.getTime())) return String(ts)
  const now = Date.now()
  const diff = Math.round((now - d.getTime()) / 1000)
  if (diff < 60) return t('time.secondsAgo', { count: diff })
  if (diff < 3600) return t('time.minutesAgo', { count: Math.round(diff / 60) })
  if (diff < 86400) return t('time.hoursAgo', { count: Math.round(diff / 3600) })
  return t('time.daysAgo', { count: Math.round(diff / 86400) })
}

function taskTone(state: string): 'green' | 'red' | 'yellow' {
  if (state === 'completed') return 'green'
  if (state === 'failed') return 'red'
  return 'yellow'
}

function decodeResponse(b64?: string): string {
  if (!b64) return ''
  try {
    const bin = atob(b64)
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
    return new TextDecoder('utf-8', { fatal: false }).decode(bytes)
  } catch {
    return b64
  }
}

export default function BeaconDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const toast = useToast()
  const [beacon, setBeacon] = useState<Beacon | null>(null)
  const [error, setError] = useState('')
  const [tasks, setTasks] = useState<BeaconTask[]>([])
  const [selected, setSelected] = useState<BeaconTask | null>(null)
  const [content, setContent] = useState('')
  const [loadingContent, setLoadingContent] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [busy, setBusy] = useState(false)
  const [opening, setOpening] = useState(false)

  const openSession = async () => {
    if (!beacon) return
    setOpening(true)
    try {
      const res = await api.openSessionFromBeacon(beacon.ID)
      if (res.async) {
        toast.push('success', t('beacons.sessionQueued', { name: beacon.Name }))
      } else {
        toast.push('success', t('beacons.sessionOpened'))
      }
      navigate('/sessions')
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setOpening(false)
    }
  }

  const load = useCallback(async () => {
    if (!id) return
    try {
      const b = await api.beacon(id)
      setBeacon(b)
      setError('')
    } catch (e) {
      setBeacon(null)
      setError(t('detail.notFound'))
    }
  }, [id, t])

  const loadTasks = useCallback(async () => {
    if (!id) return
    try {
      const d = await api.beaconTasks(id)
      setTasks((d.tasks || []).sort((a, b) => b.CreatedAt - a.CreatedAt))
    } catch (e) {
      setError((e as Error).message)
    }
  }, [id])

  useEffect(() => {
    load()
    const t1 = setInterval(load, 5000)
    return () => clearInterval(t1)
  }, [load])

  useEffect(() => {
    loadTasks()
    const t2 = setInterval(loadTasks, 5000)
    return () => clearInterval(t2)
  }, [loadTasks])

  const rename = async (name: string) => {
    if (!beacon) return
    try {
      await api.renameBeacon(beacon.ID, name)
      setBeacon({ ...beacon, Name: name })
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const open = async (task: BeaconTask) => {
    setSelected(task)
    setLoadingContent(true)
    setContent('')
    try {
      const full = await api.beaconTaskContent(task.BeaconID, task.ID)
      setContent(decodeResponse(full.ResponseB64))
    } catch (e) {
      setContent(`${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setLoadingContent(false)
    }
  }

  const remove = async () => {
    if (!beacon) return
    setBusy(true)
    try {
      await api.rmBeacon(beacon.ID)
      toast.push('success', t('beacons.removed', { name: beacon.Name }))
      navigate('/beacons')
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const columns: Column<BeaconTask>[] = [
    {
      key: 'State',
      label: t('tasks.thState'),
      sortable: true,
      render: (task) => <StatusBadge tone={taskTone(task.State)}>{task.State}</StatusBadge>,
    },
    {
      key: 'Description',
      label: t('tasks.thDescription'),
      sortable: true,
      render: (task) => <span className="mono task-desc">{task.Description}</span>,
    },
    {
      key: 'CreatedAt',
      label: t('tasks.thCreated'),
      sortable: true,
      sortValue: (task) => task.CreatedAt,
      render: (task) => fmtTime(task.CreatedAt, t),
    },
    {
      key: 'CompletedAt',
      label: t('tasks.thCompleted'),
      sortable: true,
      sortValue: (task) => task.CompletedAt || 0,
      render: (task) => fmtTime(task.CompletedAt, t),
    },
  ]

  return (
    <div className="page page-detail">
      {error && <div className="error-banner">{error}</div>}
      {beacon && (
        <>
          <div className="issue-header">
            <div className="issue-title-block">
              <div className="issue-title-row">
                <InlineEdit value={beacon.Name} onSave={rename} mono />
                <StatusBadge tone="blue">BEACON</StatusBadge>
              </div>
              <div className="page-sub">
                {beacon.Hostname} · {beacon.RemoteAddress} · {beacon.Transport}
              </div>
            </div>
            <div className="toolbar">
              <button type="button" className="btn" onClick={() => navigate('/beacons')}>
                {t('detail.back')}
              </button>
            </div>
          </div>

          <div className="issue-layout">
            <div className="issue-main">
              <div className="card card-flush">
                <div className="card-title" style={{ padding: '12px 16px 0' }}>
                  {t('beacons.tasks')}
                </div>
                <DataTable
                  columns={columns}
                  rows={tasks}
                  rowKey={(task) => task.ID}
                  searchable
                  searchPlaceholder={t('tasks.search')}
                  searchText={(task) => `${task.Description} ${task.State}`}
                  empty={t('tasks.empty')}
                  onRowClick={open}
                  navigable
                />
              </div>
            </div>
            <aside className="issue-side">
              <div className="side-card">
                <div className="side-card-title">{t('detail.sidebar.properties')}</div>
                <div className="side-row">
                  <span className="side-label">ID</span>
                  <span className="side-value mono">{beacon.ID}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('detail.sidebar.hostname')}</span>
                  <span className="side-value">{beacon.Hostname}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('detail.sidebar.user')}</span>
                  <span className="side-value">{beacon.Username}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('detail.sidebar.os')}</span>
                  <span className="side-value">{beacon.OS}/{beacon.Arch}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('detail.sidebar.transport')}</span>
                  <span className="side-value">{beacon.Transport}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('detail.sidebar.remote')}</span>
                  <span className="side-value mono">{beacon.RemoteAddress}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('detail.sidebar.c2')}</span>
                  <span className="side-value mono">{beacon.ActiveC2}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('beacons.thLastCheckin')}</span>
                  <span className="side-value">{fmtTime(beacon.LastCheckin, t)}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('beacons.thNextCheckin')}</span>
                  <span className="side-value">{fmtTime(beacon.NextCheckin, t)}</span>
                </div>
                <div className="side-row">
                  <span className="side-label">{t('beacons.thInterval')}</span>
                  <span className="side-value mono">
                    {beacon.Interval}s / {beacon.Jitter}%
                  </span>
                </div>
              </div>
              <div className="side-actions">
                <button type="button" className="btn" onClick={openSession} disabled={opening}>
                  {opening ? t('common.loading') : t('beacons.openSession')}
                </button>
                <button type="button" className="btn danger" onClick={() => setRemoving(true)}>
                  {t('beacons.remove')}
                </button>
              </div>
            </aside>
          </div>
        </>
      )}
      {!error && !beacon && <div className="empty">{t('common.loading')}</div>}
      <DetailDrawer
        open={!!selected}
        title={t('tasks.detail')}
        subtitle={selected ? `${beacon?.Name || ''} · ${selected.ID}` : ''}
        onClose={() => setSelected(null)}
      >
        {selected && (
          <div className="task-detail">
            <div className="kv">
              <div className="side-row">
                <span className="side-label">{t('tasks.thState')}</span>
                <StatusBadge tone={taskTone(selected.State)}>{selected.State}</StatusBadge>
              </div>
              <div className="side-row">
                <span className="side-label">{t('tasks.thCreated')}</span>
                <span className="side-value">{fmtTime(selected.CreatedAt, t)}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('tasks.thCompleted')}</span>
                <span className="side-value">{fmtTime(selected.CompletedAt, t)}</span>
              </div>
            </div>
            <div className="task-content">
              <div className="task-content-header">
                <span>{t('tasks.content')}</span>
              </div>
              {loadingContent ? <pre>{t('common.loading')}</pre> : <pre>{content || t('tasks.none')}</pre>}
            </div>
          </div>
        )}
      </DetailDrawer>
      <ConfirmDialog
        open={removing}
        title={t('beacons.confirmRemove')}
        danger
        busy={busy}
        confirmLabel={t('beacons.remove')}
        onConfirm={remove}
        onCancel={() => setRemoving(false)}
      >
        <p>{beacon ? t('beacons.confirmRemoveBody', { name: beacon.Name }) : ''}</p>
      </ConfirmDialog>
    </div>
  )
}
