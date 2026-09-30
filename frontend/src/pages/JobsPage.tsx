import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Job } from '../lib/types'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import ConfirmDialog from '../components/common/ConfirmDialog'
import { useToast } from '../components/common/Toast'
import './pages.css'

export default function JobsPage() {
  const { t } = useTranslation()
  const toast = useToast()
  const [jobs, setJobs] = useState<Job[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [stopping, setStopping] = useState<Job | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      const data = await api.jobs()
      setJobs(data.jobs || [])
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

  const columns: Column<Job>[] = [
    { key: 'ID', label: t('listeners.thId'), sortable: true, render: (j) => <span className="mono">{j.ID}</span> },
    { key: 'Name', label: t('listeners.thName'), sortable: true, render: (j) => <span className="mono">{j.Name}</span> },
    {
      key: 'Protocol',
      label: t('listeners.thProtocol'),
      sortable: true,
      render: (j) => <StatusBadge tone="blue">{j.Protocol}</StatusBadge>,
    },
    { key: 'Port', label: t('listeners.thPort'), sortable: true, render: (j) => <span className="mono">{j.Port}</span> },
    {
      key: 'Domains',
      label: t('listeners.thDomains'),
      render: (j) => <span className="mono">{j.Domains?.join(', ') || '-'}</span>,
    },
    {
      key: 'Control',
      label: '',
      render: (j) => (
        <button type="button" className="btn sm danger" onClick={() => setStopping(j)}>
          {t('listeners.stop')}
        </button>
      ),
    },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('jobs.title')}</div>
          <div className="page-sub">{t('jobs.sub', { count: jobs.length })}</div>
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
          rows={jobs}
          rowKey={(j) => j.ID}
          loading={loading}
          empty={t('jobs.empty')}
        />
      </div>
      <ConfirmDialog
        open={!!stopping}
        title={t('jobs.confirmStop')}
        danger
        busy={busy}
        confirmLabel={t('listeners.stop')}
        onConfirm={() => stopping && stop(stopping)}
        onCancel={() => setStopping(null)}
      >
        {stopping && (
          <p>
            {t('jobs.confirmStopBody', { name: stopping.Name || stopping.Protocol, id: stopping.ID })}
          </p>
        )}
      </ConfirmDialog>
    </div>
  )
}
