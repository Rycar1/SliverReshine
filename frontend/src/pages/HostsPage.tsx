import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Host } from '../lib/types'
import ConfirmDialog from '../components/common/ConfirmDialog'
import { useToast } from '../components/common/Toast'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import './pages.css'

export default function HostsPage() {
  const { t } = useTranslation()
  const toast = useToast()
  const [hosts, setHosts] = useState<Host[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [removing, setRemoving] = useState<{ kind: 'host' | 'ioc'; host: Host; ioc?: { ID: string; Path: string } } | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      const d = await api.hosts()
      setHosts(d.hosts || [])
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

  const remove = async () => {
    if (!removing) return
    setBusy(true)
    try {
      if (removing.kind === 'host') {
        await api.hostRemove(removing.host.HostUUID)
        toast.push('success', t('hosts.removed', { name: removing.host.Hostname || removing.host.HostUUID }))
      } else if (removing.ioc) {
        await api.hostIOCRm(removing.host.HostUUID, removing.ioc.ID)
        toast.push('success', t('hosts.iocRemoved', { path: removing.ioc.Path }))
      }
      setRemoving(null)
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const columns: Column<Host>[] = [
    {
      key: 'Hostname',
      label: t('hosts.thHostname'),
      sortable: true,
      render: (h) => <span className="mono">{h.Hostname || '—'}</span>,
    },
    {
      key: 'HostUUID',
      label: t('hosts.thUuid'),
      sortable: true,
      render: (h) => <span className="mono dim">{shortUuid(h.HostUUID)}</span>,
    },
    {
      key: 'OSVersion',
      label: t('hosts.thOs'),
      sortable: true,
      render: (h) => <span className="mono">{h.OSVersion || '—'}</span>,
    },
    {
      key: 'IOCs',
      label: t('hosts.thIocs'),
      sortable: true,
      sortValue: (h) => (h.IOCs || []).length,
      render: (h) => <StatusBadge tone="yellow">{h.IOCs?.length || 0}</StatusBadge>,
    },
    {
      key: '_actions',
      label: '',
      render: (h) => (
        <button type="button"
          className="btn sm danger"
          onClick={() => setRemoving({ kind: 'host', host: h })}
        >
          {t('hosts.removeHost')}
        </button>
      ),
    },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('hosts.title')}</div>
          <div className="page-sub">{t('hosts.sub', { count: hosts.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {!loading && hosts.length === 0 && !error ? (
        <div className="empty">{t('hosts.empty')}</div>
      ) : (
        <div className="card card-flush">
          <DataTable
            columns={columns}
            rows={hosts}
            rowKey={(h) => h.HostUUID}
            searchable
            searchPlaceholder={t('hosts.search')}
            searchText={(h) => `${h.Hostname} ${h.HostUUID} ${h.OSVersion}`}
            loading={loading}
            empty={t('hosts.empty')}
          />
        </div>
      )}

      {hosts.length > 0 && (
        <div className="card" style={{ marginTop: 14 }}>
          <div className="card-title">{t('hosts.iocTitle')}</div>
          {hosts.some((h) => h.IOCs?.length) ? (
            <table className="data">
              <thead>
                <tr>
                  <th>{t('hosts.thHostname')}</th>
                  <th>{t('hosts.thPath')}</th>
                  <th>{t('hosts.thHash')}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {hosts.flatMap((h) =>
                  (h.IOCs || []).map((ioc) => (
                    <tr key={ioc.ID}>
                      <td className="mono">{h.Hostname || shortUuid(h.HostUUID)}</td>
                      <td className="mono">{ioc.Path}</td>
                      <td className="mono dim">{shortHash(ioc.FileHash)}</td>
                      <td>
                        <button type="button"
                          className="btn sm danger"
                          onClick={() => setRemoving({ kind: 'ioc', host: h, ioc })}
                        >
                          {t('hosts.removeIoc')}
                        </button>
                      </td>
                    </tr>
                  )),
                )}
              </tbody>
            </table>
          ) : (
            <div className="empty">{t('hosts.noIocs')}</div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={!!removing}
        title={removing?.kind === 'host' ? t('hosts.confirmRemove') : t('hosts.confirmRemoveIoc')}
        danger
        busy={busy}
        confirmLabel={t('common.confirm')}
        onConfirm={remove}
        onCancel={() => setRemoving(null)}
      >
        <p>
          {removing?.kind === 'host'
            ? t('hosts.confirmRemoveBody', {
                name: removing.host.Hostname || removing.host.HostUUID,
              })
            : removing?.ioc
              ? t('hosts.confirmRemoveIocBody', { path: removing.ioc.Path })
              : ''}
        </p>
      </ConfirmDialog>
    </div>
  )
}

function shortUuid(uuid: string): string {
  return uuid.length > 12 ? `${uuid.slice(0, 12)}…` : uuid
}

function shortHash(hash: string): string {
  if (!hash) return '—'
  return hash.length > 16 ? `${hash.slice(0, 16)}…` : hash
}
