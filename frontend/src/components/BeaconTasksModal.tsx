import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { BeaconTask } from '../lib/types'
import '../pages/pages.css'

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

export default function BeaconTasksModal({
  beaconId,
  beaconName,
  onClose,
}: {
  beaconId: string
  beaconName: string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [tasks, setTasks] = useState<BeaconTask[]>([])
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<BeaconTask | null>(null)

  const load = useCallback(async () => {
    setError('')
    try {
      const d = await api.beaconTasks(beaconId)
      setTasks(d.tasks || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [beaconId])

  useEffect(() => {
    load()
  }, [load])

  const select = async (task: BeaconTask) => {
    setError('')
    try {
      const full = await api.beaconTaskContent(beaconId, task.ID)
      setSelected(full)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div>
            <div className="modal-title">{t('tasks.title')}</div>
            <div className="modal-sub mono">{beaconName}</div>
          </div>
          <div className="modal-actions">
            <button type="button" className="btn sm" onClick={load}>
              {t('tasks.refresh')}
            </button>
            <button type="button" className="btn sm" onClick={onClose}>
              {t('tasks.close')}
            </button>
          </div>
        </div>
        {error && <div className="error-banner">{error}</div>}
        <div className="modal-body">
          {tasks.length === 0 ? (
            <div className="empty">{t('tasks.empty')}</div>
          ) : (
            <>
              <div className="task-list">
                {tasks.map((task) => (
                  <button type="button"
                    key={task.ID}
                    className={`task-row ${selected?.ID === task.ID ? 'active' : ''}`}
                    onClick={() => select(task)}
                  >
                    <span className={`badge ${task.State === 'completed' ? 'green' : task.State === 'failed' ? 'red' : 'blue'}`}>
                      {task.State}
                    </span>
                    <span className="mono task-desc">{task.Description}</span>
                    <span className="task-time">{fmtTime(task.CreatedAt)}</span>
                  </button>
                ))}
              </div>
              {selected && (
                <div className="task-content">
                  <div className="task-content-header">
                    <span className="mono">{selected.ID}</span>
                    <span>{t('tasks.content')}</span>
                  </div>
                  <pre>{decodeResponse(selected.ResponseB64) || t('tasks.none')}</pre>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
