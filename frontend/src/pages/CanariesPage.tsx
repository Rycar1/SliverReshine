import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Canary } from '../lib/types'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import EmptyState from '../components/common/EmptyState'
import './pages.css'

export default function CanariesPage() {
  const { t } = useTranslation()
  const [canaries, setCanaries] = useState<Canary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [burnedOnly, setBurnedOnly] = useState(false)

  const load = async () => {
    try {
      const d = await api.canaries()
      setCanaries(d.canaries || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    const timer = setInterval(load, 10000)
    return () => clearInterval(timer)
  }, [])

  const filtered = burnedOnly ? canaries.filter((c) => c.Triggered) : canaries

  const columns: Column<Canary>[] = [
    {
      key: 'Domain',
      label: t('canaries.thDomain'),
      sortable: true,
      render: (c) => <span className="mono">{c.Domain}</span>,
    },
    {
      key: 'ImplantName',
      label: t('canaries.thImplant'),
      sortable: true,
      render: (c) => <span className="mono">{c.ImplantName || '—'}</span>,
    },
    {
      key: 'Triggered',
      label: t('canaries.thStatus'),
      sortable: true,
      render: (c) => (
        <StatusBadge tone={c.Triggered ? 'red' : 'green'}>
          {c.Triggered ? t('canaries.burned') : t('canaries.clean')}
        </StatusBadge>
      ),
    },
    {
      key: 'Count',
      label: t('canaries.thCount'),
      sortable: true,
      sortValue: (c) => c.Count || 0,
      render: (c) => <span className="mono">{c.Count || 0}</span>,
    },
    {
      key: 'FirstTriggered',
      label: t('canaries.thFirstTriggered'),
      sortable: true,
      render: (c) => <span className="mono">{c.FirstTriggered || '—'}</span>,
    },
    {
      key: 'LatestTrigger',
      label: t('canaries.thLatestTrigger'),
      sortable: true,
      render: (c) => <span className="mono">{c.LatestTrigger || '—'}</span>,
    },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('canaries.title')}</div>
          <div className="page-sub">{t('canaries.sub', { count: canaries.length })}</div>
        </div>
        <div className="toolbar">
          <div className="seg">
            <button type="button" className={!burnedOnly ? 'active' : ''} onClick={() => setBurnedOnly(false)}>
              {t('canaries.filterAll')}
            </button>
            <button type="button" className={burnedOnly ? 'active' : ''} onClick={() => setBurnedOnly(true)}>
              {t('canaries.filterBurned')}
            </button>
          </div>
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {!loading && filtered.length === 0 && !error ? (
        <EmptyState title={t('canaries.empty')} subtitle={t('canaries.emptySub')} />
      ) : (
        <div className="card card-flush">
          <DataTable
            columns={columns}
            rows={filtered}
            rowKey={(c) => c.Domain + c.ImplantName}
            searchable
            searchPlaceholder={t('canaries.search')}
            searchText={(c) => `${c.Domain} ${c.ImplantName}`}
            loading={loading}
            empty={t('canaries.empty')}
          />
        </div>
      )}
    </div>
  )
}
