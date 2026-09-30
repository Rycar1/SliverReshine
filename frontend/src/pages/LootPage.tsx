import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { LootEntry } from '../lib/types'
import { base64ToBytes, triggerDownload } from '../lib/binary'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import ConfirmDialog from '../components/common/ConfirmDialog'
import DetailDrawer from '../components/common/DetailDrawer'
import { useToast } from '../components/common/Toast'
import EmptyState from '../components/common/EmptyState'
import './pages.css'

function fmtSize(size: number): string {
  if (!size) return '-'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(1)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function toneForType(t: string): 'green' | 'blue' | 'yellow' {
  if (t.includes('CREDENTIAL')) return 'green'
  if (t.includes('FILE')) return 'blue'
  return 'yellow'
}

function isTextFile(name: string): boolean {
  return /\.(txt|log|json|csv|xml|yml|yaml|ini|conf|sh|bat|ps1|py|md|html?|js|ts)$/i.test(name)
}

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

export default function LootPage() {
  const { t } = useTranslation()
  const toast = useToast()
  const [loot, setLoot] = useState<LootEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState<'all' | 'FILE' | 'CREDENTIAL'>('all')
  const [selected, setSelected] = useState<LootEntry | null>(null)
  const [content, setContent] = useState('')
  const [loadingContent, setLoadingContent] = useState(false)
  const [removing, setRemoving] = useState<LootEntry | null>(null)
  const [busy, setBusy] = useState(false)
  const [fullEntry, setFullEntry] = useState<LootEntry | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [addBusy, setAddBusy] = useState(false)
  const [addType, setAddType] = useState<'file' | 'credential'>('file')
  const [addName, setAddName] = useState('')
  const [addFile, setAddFile] = useState<File | null>(null)
  const [addCredType, setAddCredType] = useState<'up' | 'apikey'>('up')
  const [addUser, setAddUser] = useState('')
  const [addPassword, setAddPassword] = useState('')
  const [addApiKey, setAddApiKey] = useState('')
  const [renaming, setRenaming] = useState(false)
  const [renameValue, setRenameValue] = useState('')

  const load = async () => {
    try {
      const d = await api.lootList(filter === 'all' ? undefined : filter.toLowerCase())
      setLoot(d.loot || [])
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
  }, [filter])

  const filtered = useMemo(
    () => (filter === 'all' ? loot : loot.filter((l) => (l.LootType || '').includes(filter))),
    [loot, filter],
  )

  const open = async (entry: LootEntry) => {
    setSelected(entry)
    setLoadingContent(true)
    setContent('')
    setFullEntry(null)
    setRenaming(false)
    setRenameValue(entry.Name)
    try {
      const full = await api.lootContent(entry.ID)
      setFullEntry(full)
      const b64 = full.DataB64 || ''
      setContent(
        (entry.FileType || '').includes('TEXT') ? new TextDecoder('utf-8', { fatal: false }).decode(base64ToBytes(b64)) : `${t('loot.binaryHint')} (${fmtSize(b64.length * 0.75)})`,
      )
    } catch (e) {
      setContent(`${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setLoadingContent(false)
    }
  }

  const download = async (entry: LootEntry) => {
    try {
      const full = await api.lootContent(entry.ID)
      if (!full.DataB64) return
      triggerDownload(entry.File || entry.Name || 'loot', base64ToBytes(full.DataB64))
      toast.push('success', t('loot.downloaded', { name: entry.Name }))
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  const remove = async (entry: LootEntry) => {
    setBusy(true)
    try {
      await api.lootRemove(entry.ID)
      setRemoving(null)
      if (selected?.ID === entry.ID) setSelected(null)
      toast.push('success', t('loot.removed', { name: entry.Name }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const addLoot = async () => {
    if (!addName) {
      toast.push('error', t('loot.nameRequired'))
      return
    }
    setAddBusy(true)
    try {
      const body: Parameters<typeof api.lootAdd>[0] = { type: addType, name: addName }
      if (addType === 'file') {
        if (!addFile) {
          toast.push('error', t('loot.fileRequired'))
          setAddBusy(false)
          return
        }
        body.file_name = addFile.name
        body.file_type = isTextFile(addFile.name) ? 'text' : 'binary'
        body.file_data_b64 = await fileToBase64(addFile)
      } else if (addCredType === 'apikey') {
        body.cred_api_key = addApiKey
      } else {
        body.cred_user = addUser
        body.cred_password = addPassword
      }
      await api.lootAdd(body)
      toast.push('success', t('loot.added', { name: addName }))
      setAddOpen(false)
      setAddName('')
      setAddFile(null)
      setAddUser('')
      setAddPassword('')
      setAddApiKey('')
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setAddBusy(false)
    }
  }

  const renameLoot = async () => {
    if (!selected || !renameValue.trim()) return
    try {
      await api.lootRename(selected.ID, renameValue.trim())
      setSelected({ ...selected, Name: renameValue.trim() })
      setRenaming(false)
      toast.push('success', t('loot.renamed', { name: renameValue.trim() }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  const copyText = async (text: string, name: string) => {
    try {
      await navigator.clipboard.writeText(text || '')
      toast.push('success', t('loot.copied', { name }))
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  const columns: Column<LootEntry>[] = [
    {
      key: 'Name',
      label: t('loot.thName'),
      sortable: true,
      render: (l) => (
        <span className="mono">
          {l.Name}
          {l.File && <span className="loot-file"> · {l.File}</span>}
        </span>
      ),
    },
    {
      key: 'LootType',
      label: t('loot.thType'),
      sortable: true,
      render: (l) => <StatusBadge tone={toneForType(l.LootType)}>{l.LootType}</StatusBadge>,
    },
    {
      key: 'FileType',
      label: t('loot.thFileType'),
      sortable: true,
      render: (l) => <span className="mono">{l.FileType}</span>,
    },
    {
      key: 'Size',
      label: t('loot.thSize'),
      sortable: true,
      sortValue: (l) => l.Size || 0,
      render: (l) => <span className="mono">{fmtSize(l.Size)}</span>,
    },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('loot.title')}</div>
          <div className="page-sub">{t('loot.sub', { count: loot.length })}</div>
        </div>
        <div className="toolbar">
          <div className="seg">
            {(['all', 'FILE', 'CREDENTIAL'] as const).map((f) => (
              <button type="button" key={f} className={filter === f ? 'active' : ''} onClick={() => setFilter(f)}>
                {f === 'all' ? t('loot.filterAll') : f === 'FILE' ? t('loot.filterFiles') : t('loot.filterCreds')}
              </button>
            ))}
          </div>
          <button type="button" className="btn primary" onClick={() => setAddOpen(true)}>
            {t('loot.add')}
          </button>
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {!loading && filtered.length === 0 && !error ? (
        <EmptyState title={t('loot.empty')} subtitle={t('loot.emptySub')} />
      ) : (
        <div className="card card-flush">
          <DataTable
            columns={columns}
            rows={filtered}
            rowKey={(l) => l.ID}
            searchable
            searchPlaceholder={t('loot.search')}
            searchText={(l) => `${l.Name} ${l.File} ${l.LootType} ${l.FileType}`}
            loading={loading}
            empty={t('loot.empty')}
            onRowClick={open}
            navigable
          />
        </div>
      )}
      <DetailDrawer
        open={!!selected}
        title={selected ? selected.Name : ''}
        subtitle={selected ? selected.ID : ''}
        onClose={() => setSelected(null)}
        footer={
          selected && (
            <div className="drawer-actions">
              {selected.LootType.includes('FILE') && (
                <button type="button" className="btn" onClick={() => download(selected)}>
                  {t('loot.download')}
                </button>
              )}
              <button type="button" className="btn danger" onClick={() => setRemoving(selected)}>
                {t('loot.remove')}
              </button>
            </div>
          )
        }
      >
        {selected && (
          <div className="drawer-detail">
            <div className="drawer-section">{t('loot.metadata')}</div>
            <div className="drow">
              <span className="dlabel">{t('loot.thName')}</span>
              {renaming ? (
                <div className="drawer-rename">
                  <input className="input" value={renameValue} onChange={(e) => setRenameValue(e.target.value)} />
                  <button type="button" className="btn sm primary" onClick={renameLoot} disabled={!renameValue.trim()}>
                    {t('common.save')}
                  </button>
                  <button type="button"
                    className="btn sm"
                    onClick={() => {
                      setRenaming(false)
                      setRenameValue(selected.Name)
                    }}
                  >
                    {t('common.cancel')}
                  </button>
                </div>
              ) : (
                <div className="drawer-rename">
                  <span className="dvalue">{selected.Name}</span>
                  <button type="button" className="btn sm" onClick={() => setRenaming(true)}>
                    {t('loot.rename')}
                  </button>
                </div>
              )}
            </div>
            <div className="drow">
              <span className="dlabel">{t('loot.thType')}</span>
              <StatusBadge tone={toneForType(selected.LootType)}>{selected.LootType}</StatusBadge>
            </div>
            <div className="drow">
              <span className="dlabel">{t('loot.thFileType')}</span>
              <span className="dvalue mono">{selected.FileType}</span>
            </div>
            <div className="drow">
              <span className="dlabel">{t('loot.thSize')}</span>
              <span className="dvalue mono">{fmtSize(selected.Size)}</span>
            </div>
            {selected.File && (
              <div className="drow">
                <span className="dlabel">{t('loot.thFile')}</span>
                <span className="dvalue mono">{selected.File}</span>
              </div>
            )}
            {selected.LootType.includes('CREDENTIAL') && (
              <>
                <div className="drawer-section">{t('loot.filterCreds')}</div>
                <div className="drow">
                  <span className="dlabel">{t('loot.credUser')}</span>
                  <div className="drawer-copy">
                    <span className="dvalue mono">{(fullEntry || selected).CredUser || '—'}</span>
                    <button type="button" className="btn sm" onClick={() => copyText((fullEntry || selected).CredUser || '', t('loot.credUser'))}>
                      {t('loot.copy')}
                    </button>
                  </div>
                </div>
                <div className="drow">
                  <span className="dlabel">{t('loot.credPassword')}</span>
                  <div className="drawer-copy">
                    <span className="dvalue mono">{(fullEntry || selected).CredPassword || '—'}</span>
                    <button type="button" className="btn sm" onClick={() => copyText((fullEntry || selected).CredPassword || '', t('loot.credPassword'))}>
                      {t('loot.copy')}
                    </button>
                  </div>
                </div>
                <div className="drow">
                  <span className="dlabel">{t('loot.credApiKey')}</span>
                  <div className="drawer-copy">
                    <span className="dvalue mono">{(fullEntry || selected).CredAPIKey || '—'}</span>
                    <button type="button" className="btn sm" onClick={() => copyText((fullEntry || selected).CredAPIKey || '', t('loot.credApiKey'))}>
                      {t('loot.copy')}
                    </button>
                  </div>
                </div>
              </>
            )}
            {selected.LootType.includes('FILE') && (
              <>
                <div className="drawer-section">{t('loot.content')}</div>
                {loadingContent ? <pre className="viewer-pre">{t('common.loading')}</pre> : <pre className="viewer-pre">{content || t('tasks.none')}</pre>}
              </>
            )}
          </div>
        )}
      </DetailDrawer>
      <DetailDrawer
        open={addOpen}
        title={t('loot.addTitle')}
        subtitle={t('loot.addSub')}
        onClose={() => setAddOpen(false)}
        footer={
          <div className="drawer-actions">
            <button type="button" className="btn" onClick={() => setAddOpen(false)}>
              {t('common.cancel')}
            </button>
            <button type="button" className="btn primary" disabled={addBusy} onClick={addLoot}>
              {t('loot.add')}
            </button>
          </div>
        }
      >
        <div className="drawer-detail">
          <div className="drawer-section">{t('loot.addType')}</div>
          <div className="seg" style={{ alignSelf: 'flex-start' }}>
            <button type="button" className={addType === 'file' ? 'active' : ''} onClick={() => setAddType('file')}>
              {t('loot.filterFiles')}
            </button>
            <button type="button" className={addType === 'credential' ? 'active' : ''} onClick={() => setAddType('credential')}>
              {t('loot.filterCreds')}
            </button>
          </div>
          <div className="field">
            <label>{t('loot.thName')}</label>
            <input value={addName} onChange={(e) => setAddName(e.target.value)} placeholder={t('loot.namePlaceholder')} />
          </div>
          {addType === 'file' ? (
            <div className="field">
              <label>{t('loot.fileField')}</label>
              <input type="file" onChange={(e) => setAddFile(e.target.files?.[0] || null)} />
            </div>
          ) : (
            <>
              <div className="seg" style={{ alignSelf: 'flex-start' }}>
                <button type="button" className={addCredType === 'up' ? 'active' : ''} onClick={() => setAddCredType('up')}>
                  {t('loot.credUp')}
                </button>
                <button type="button" className={addCredType === 'apikey' ? 'active' : ''} onClick={() => setAddCredType('apikey')}>
                  {t('loot.credApiKey')}
                </button>
              </div>
              {addCredType === 'up' ? (
                <>
                  <div className="field">
                    <label>{t('loot.credUser')}</label>
                    <input value={addUser} onChange={(e) => setAddUser(e.target.value)} />
                  </div>
                  <div className="field">
                    <label>{t('loot.credPassword')}</label>
                    <input type="password" value={addPassword} onChange={(e) => setAddPassword(e.target.value)} />
                  </div>
                </>
              ) : (
                <div className="field">
                  <label>{t('loot.credApiKey')}</label>
                  <input value={addApiKey} onChange={(e) => setAddApiKey(e.target.value)} />
                </div>
              )}
            </>
          )}
        </div>
      </DetailDrawer>
      <ConfirmDialog
        open={!!removing}
        title={t('loot.confirmRemove')}
        danger
        busy={busy}
        confirmLabel={t('loot.remove')}
        onConfirm={() => removing && remove(removing)}
        onCancel={() => setRemoving(null)}
      >
        <p>{removing ? t('loot.confirmRemoveBody', { name: removing.Name }) : ''}</p>
      </ConfirmDialog>
    </div>
  )
}
