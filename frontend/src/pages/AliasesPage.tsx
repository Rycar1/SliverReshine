import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Alias, Session } from '../lib/types'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import ConfirmDialog from '../components/common/ConfirmDialog'
import DetailDrawer from '../components/common/DetailDrawer'
import { useToast } from '../components/common/Toast'
import EmptyState from '../components/common/EmptyState'
import './pages.css'

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      resolve(result.split(',')[1] || '')
    }
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}

export default function AliasesPage() {
  const { t } = useTranslation()
  const toast = useToast()
  const [aliases, setAliases] = useState<Alias[]>([])
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<Alias | null>(null)
  const [removing, setRemoving] = useState<Alias | null>(null)
  const [busy, setBusy] = useState(false)
  const [installOpen, setInstallOpen] = useState(false)
  const [installBusy, setInstallBusy] = useState(false)
  const [installFile, setInstallFile] = useState<File | null>(null)

  const [runSession, setRunSession] = useState('')
  const [runArgs, setRunArgs] = useState('')
  const [runProcess, setRunProcess] = useState('')
  const [runArch, setRunArch] = useState('x84')
  const [runMethod, setRunMethod] = useState('')
  const [runClass, setRunClass] = useState('')
  const [runBusy, setRunBusy] = useState(false)
  const [runOutput, setRunOutput] = useState('')
  const [runMeta, setRunMeta] = useState('')

  const load = async () => {
    try {
      const [a, s] = await Promise.all([api.aliases(), api.sessions()])
      setAliases(a.aliases || [])
      setSessions(s.sessions || [])
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

  const open = async (a: Alias) => {
    setSelected(a)
    setRunSession('')
    setRunArgs('')
    setRunProcess('')
    setRunArch('x84')
    setRunMethod('')
    setRunClass('')
    setRunOutput('')
    setRunMeta('')
  }

  const install = async () => {
    if (!installFile) {
      toast.push('error', t('aliases.bundleRequired'))
      return
    }
    setInstallBusy(true)
    try {
      const b64 = await fileToBase64(installFile)
      const res = await api.aliasInstall(b64)
      toast.push('success', t('aliases.installed', { name: res.alias.Name }))
      setInstallOpen(false)
      setInstallFile(null)
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setInstallBusy(false)
    }
  }

  const remove = async (a: Alias) => {
    setBusy(true)
    try {
      await api.aliasRemove(a.CommandName)
      if (selected?.CommandName === a.CommandName) setSelected(null)
      toast.push('success', t('aliases.removed', { name: a.Name }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const run = async () => {
    if (!selected) return
    if (!runSession) {
      toast.push('error', t('aliases.sessionRequired'))
      return
    }
    setRunBusy(true)
    setRunOutput('')
    setRunMeta('')
    try {
      const res = await api.aliasRun(runSession, selected.CommandName, {
        args: runArgs,
        process: runProcess,
        arch: selected.IsAssembly ? runArch : undefined,
        method: selected.IsAssembly ? runMethod : undefined,
        class: selected.IsAssembly ? runClass : undefined,
      })
      setRunOutput(res.output || '')
      setRunMeta(
        `${res.command} · ${res.mode} · ${res.platform} · ${res.process || 'default proc'}${res.args ? ` · args: ${res.args}` : ''}`,
      )
    } catch (e) {
      setRunOutput(`${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setRunBusy(false)
    }
  }

  const columns: Column<Alias>[] = [
    {
      key: 'CommandName',
      label: t('aliases.thCommand'),
      sortable: true,
      render: (a) => <span className="mono">{a.CommandName}</span>,
    },
    {
      key: 'Name',
      label: t('aliases.thName'),
      sortable: true,
      render: (a) => <span>{a.Name}</span>,
    },
    {
      key: 'Version',
      label: t('aliases.thVersion'),
      sortable: true,
      render: (a) => <span className="mono">{a.Version || '—'}</span>,
    },
    {
      key: 'Platforms',
      label: t('aliases.thPlatforms'),
      sortable: false,
      render: (a) => (
        <div className="platform-list">
          {(a.Platforms || []).map((p) => (
            <span key={p} className="badge blue">
              {p}
            </span>
          ))}
        </div>
      ),
    },
    {
      key: 'IsAssembly',
      label: t('aliases.thMode'),
      sortable: true,
      render: (a) => (
        <StatusBadge tone={a.IsAssembly ? 'green' : a.IsReflective ? 'yellow' : 'blue'}>
          {a.IsAssembly ? t('aliases.modeAssembly') : a.IsReflective ? t('aliases.modeDll') : t('aliases.modeSideload')}
        </StatusBadge>
      ),
    },
    {
      key: 'OriginalAuthor',
      label: t('aliases.thAuthor'),
      sortable: true,
      render: (a) => <span className="mono">{a.OriginalAuthor || '—'}</span>,
    },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('aliases.title')}</div>
          <div className="page-sub">{t('aliases.sub', { count: aliases.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn primary" onClick={() => setInstallOpen(true)}>
            {t('aliases.install')}
          </button>
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {!loading && aliases.length === 0 && !error ? (
        <EmptyState title={t('aliases.empty')} subtitle={t('aliases.emptySub')} />
      ) : (
        <div className="card card-flush">
          <DataTable
            columns={columns}
            rows={aliases}
            rowKey={(a) => a.CommandName}
            searchable
            searchPlaceholder={t('aliases.search')}
            searchText={(a) => `${a.Name} ${a.CommandName} ${a.OriginalAuthor}`}
            loading={loading}
            empty={t('aliases.empty')}
            onRowClick={open}
            navigable
          />
        </div>
      )}

      <DetailDrawer
        open={!!selected}
        title={selected ? selected.Name : ''}
        subtitle={selected ? selected.CommandName : ''}
        onClose={() => setSelected(null)}
        footer={
          selected && (
            <div className="drawer-actions">
              <button type="button" className="btn danger" onClick={() => setRemoving(selected)}>
                {t('aliases.remove')}
              </button>
            </div>
          )
        }
      >
        {selected && (
          <div className="task-detail">
            <div className="kv">
              <div className="side-row">
                <span className="side-label">{t('aliases.thVersion')}</span>
                <span className="side-value mono">{selected.Version || '—'}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('aliases.thAuthor')}</span>
                <span className="side-value mono">{selected.OriginalAuthor || '—'}</span>
              </div>
              {selected.RepoURL && (
                <div className="side-row">
                  <span className="side-label">Repository</span>
                  <span className="side-value mono">{selected.RepoURL}</span>
                </div>
              )}
              <div className="side-row">
                <span className="side-label">{t('aliases.help')}</span>
                <span className="side-value">{selected.Help || '—'}</span>
              </div>
              <div className="side-row">
                <span className="side-label">{t('aliases.thMode')}</span>
                <StatusBadge tone={selected.IsAssembly ? 'green' : selected.IsReflective ? 'yellow' : 'blue'}>
                  {selected.IsAssembly ? t('aliases.modeAssembly') : selected.IsReflective ? t('aliases.modeDll') : t('aliases.modeSideload')}
                </StatusBadge>
              </div>
              <div className="side-row">
                <span className="side-label">{t('aliases.thPlatforms')}</span>
                <div className="platform-list">
                  {(selected.Platforms || []).map((p) => (
                    <span key={p} className="badge blue">
                      {p}
                    </span>
                  ))}
                </div>
              </div>
            </div>

            <div className="task-content">
              <div className="task-content-header">
                <span>{t('aliases.run')}</span>
              </div>
              <div className="form">
                <label className="form-label">{t('aliases.targetSession')}</label>
                <select
                  className="input"
                  value={runSession}
                  onChange={(e) => setRunSession(e.target.value)}
                >
                  <option value="">{t('aliases.selectSession')}</option>
                  {sessions
                    .filter((s) => !s.IsDead)
                    .map((s) => (
                      <option key={s.ID} value={s.ID}>
                        {s.Name} · {s.Hostname} · {s.OS}/{s.Arch}
                      </option>
                    ))}
                </select>
                <label className="form-label">{t('aliases.args')}</label>
                <input
                  className="input"
                  value={runArgs}
                  onChange={(e) => setRunArgs(e.target.value)}
                  placeholder={selected.DefaultArgs || ''}
                />
                <label className="form-label">{t('aliases.process')}</label>
                <input
                  className="input"
                  value={runProcess}
                  onChange={(e) => setRunProcess(e.target.value)}
                  placeholder={t('aliases.processPlaceholder')}
                />
                {selected.IsAssembly && (
                  <>
                    <div className="seg">
                      {(['x84', 'x86', 'x64'] as const).map((a) => (
                        <button type="button" key={a} className={runArch === a ? 'active' : ''} onClick={() => setRunArch(a)}>
                          {a}
                        </button>
                      ))}
                    </div>
                    <label className="form-label">{t('aliases.method')}</label>
                    <input className="input" value={runMethod} onChange={(e) => setRunMethod(e.target.value)} />
                    <label className="form-label">{t('aliases.className')}</label>
                    <input className="input" value={runClass} onChange={(e) => setRunClass(e.target.value)} />
                  </>
                )}
                <button type="button" className="btn primary" disabled={runBusy || !runSession} onClick={run}>
                  {runBusy ? t('common.loading') : t('aliases.run')}
                </button>
                {runMeta && <div className="run-meta mono">{runMeta}</div>}
                {runOutput && <pre className="run-output">{runOutput}</pre>}
              </div>
            </div>
          </div>
        )}
      </DetailDrawer>

      <DetailDrawer
        open={installOpen}
        title={t('aliases.installTitle')}
        subtitle={t('aliases.installSub')}
        onClose={() => setInstallOpen(false)}
        footer={
          <div className="drawer-actions">
            <button type="button" className="btn" onClick={() => setInstallOpen(false)}>
              {t('common.cancel')}
            </button>
            <button type="button" className="btn primary" disabled={installBusy || !installFile} onClick={install}>
              {installBusy ? t('common.loading') : t('aliases.install')}
            </button>
          </div>
        }
      >
        <div className="form">
          <label className="form-label">{t('aliases.bundle')}</label>
          <input className="input" type="file" accept=".tar.gz,.tgz,.gz,.tar" onChange={(e) => setInstallFile(e.target.files?.[0] || null)} />
          <p className="form-hint">{t('aliases.bundleHint')}</p>
        </div>
      </DetailDrawer>

      <ConfirmDialog
        open={!!removing}
        title={t('aliases.confirmRemove')}
        danger
        busy={busy}
        confirmLabel={t('aliases.remove')}
        onConfirm={() => removing && remove(removing)}
        onCancel={() => setRemoving(null)}
      >
        <p>{removing ? t('aliases.confirmRemoveBody', { name: removing.Name }) : ''}</p>
      </ConfirmDialog>
    </div>
  )
}
