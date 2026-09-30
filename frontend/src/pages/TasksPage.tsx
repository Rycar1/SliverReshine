import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Beacon, BeaconTask } from '../lib/types'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import DetailDrawer from '../components/common/DetailDrawer'
import './pages.css'

interface Row {
  task: BeaconTask
  beaconName: string
}

function fmtTime(ts: number): string {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
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

function taskTone(state: string): 'green' | 'red' | 'yellow' {
  if (state === 'completed') return 'green'
  if (state === 'failed') return 'red'
  return 'yellow'
}

export default function TasksPage() {
  const { t } = useTranslation()
  const [rows, setRows] = useState<Row[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<Row | null>(null)
  const [content, setContent] = useState('')
  const [loadingContent, setLoadingContent] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const bd = await api.beacons()
      const beacons = bd.beacons || []
      const results = await Promise.allSettled(
        beacons.map(async (b: Beacon) => {
          const td = await api.beaconTasks(b.ID)
          return (td.tasks || []).map((task) => ({ task, beaconName: b.Name }))
        }),
      )
      const all: Row[] = []
      results.forEach((r) => {
        if (r.status === 'fulfilled') all.push(...r.value)
      })
      all.sort((a, b) => b.task.CreatedAt - a.task.CreatedAt)
      setRows(all)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [load])

  const open = async (row: Row) => {
    setSelected(row)
    setLoadingContent(true)
    setContent('')
    try {
      const full = await api.beaconTaskContent(row.task.BeaconID, row.task.ID)
      setContent(decodeResponse(full.ResponseB64))
    } catch (e) {
      setContent(`${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setLoadingContent(false)
    }
  }

  const columns: Column<Row>[] = [
    {
      key: 'beacon',
      label: t('tasks.thBeacon'),
      sortable: true,
      render: (r) => <span className="mono">{r.beaconName}</span>,
    },
    {
      key: 'state',
      label: t('tasks.thState'),
      sortable: true,
      render: (r) => <StatusBadge tone={taskTone(r.task.State)}>{r.task.State}</StatusBadge>,
    },
    {
      key: 'description',
      label: t('tasks.thDescription'),
      sortable: true,
      render: (r) => <span className="mono task-desc">{r.task.Description}</span>,
    },
    {
      key: 'created',
      label: t('tasks.thCreated'),
      sortable: true,
      sortValue: (r) => r.task.CreatedAt,
      render: (r) => fmtTime(r.task.CreatedAt),
    },
    {
      key: 'completed',
      label: t('tasks.thCompleted'),
      sortable: true,
      sortValue: (r) => r.task.CompletedAt || 0,
      render: (r) => fmtTime(r.task.CompletedAt),
    },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('tasks.title')}</div>
          <div className="page-sub">{t('tasks.sub', { count: rows.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div className="card card-flush">
        <DataTable
          columns={columns}
          rows={rows}
          rowKey={(r) => r.task.ID}
          searchable
          searchPlaceholder={t('tasks.search')}
          searchText={(r) => `${r.beaconName} ${r.task.Description} ${r.task.State}`}
          loading={loading}
          empty={t('tasks.emptyAll')}
          onRowClick={open}
          navigable
        />
      </div>
      <DetailDrawer
        open={!!selected}
        title={selected ? t('tasks.detail') : ''}
        subtitle={selected ? `${selected.beaconName} · ${selected.task.ID}` : ''}
        onClose={() => setSelected(null)}
      >
        {selected && (
          <div className="task-detail">
            <div className="kv">
              <div className="side-row">
                <span className="side-label">{t('tasks.thState')}</span>
                <StatusBadge tone={taskTone(selected.task.State)}>{selected.task.State}</StatusBadge>
              </div>
              <div className="side-row">
                <span className="side-label">{t('tasks.thCreated')}</span>
                <span className="side-value">{fmtTime(selected.task.CreatedAt)}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('tasks.thCompleted')}</span>
                <span className="side-value">{fmtTime(selected.task.CompletedAt)}</span>
              </div>
            </div>
            <div className="task-content">
              <div className="task-content-header">
                <span>{t('tasks.content')}</span>
              </div>
              {loadingContent ? (
                <pre>{t('common.loading')}</pre>
              ) : (
                <pre>{content || t('tasks.none')}</pre>
              )}
            </div>
          </div>
        )}
      </DetailDrawer>
    </div>
  )
}
