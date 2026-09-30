import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { WebContent, Website } from '../lib/types'
import DataTable, { type Column } from '../components/common/DataTable'
import ConfirmDialog from '../components/common/ConfirmDialog'
import DetailDrawer from '../components/common/DetailDrawer'
import { useToast } from '../components/common/Toast'
import './pages.css'

export default function WebsitesPage() {
  const { t } = useTranslation()
  const toast = useToast()
  const [websites, setWebsites] = useState<Website[]>([])
  const [selected, setSelected] = useState<Website | null>(null)
  const [loading, setLoading] = useState(true)
  const [detailLoading, setDetailLoading] = useState(false)
  const [error, setError] = useState('')
  const [showAdd, setShowAdd] = useState(false)
  const [editing, setEditing] = useState<WebContent | null>(null)
  const [viewing, setViewing] = useState<WebContent | null>(null)
  const [viewText, setViewText] = useState('')
  const [removing, setRemoving] = useState<{ kind: 'site' | 'content'; siteName: string; content?: WebContent } | null>(null)
  const [busy, setBusy] = useState(false)

  const load = async () => {
    try {
      setError('')
      const d = await api.websites()
      setWebsites(d.websites || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const loadDetail = async (name: string) => {
    try {
      setDetailLoading(true)
      const site = await api.website(name)
      setSelected(site)
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setDetailLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  const selectSite = async (site: Website) => {
    if (selected?.Name === site.Name) return
    setSelected(site)
    loadDetail(site.Name)
  }

  const siteSize = (site: Website) =>
    Object.values(site.Contents || {}).reduce((acc, c) => acc + (c.Size || 0), 0)

  const contentList = useMemo(() => {
    if (!selected) return []
    return Object.values(selected.Contents || {}).sort((a, b) => a.Path.localeCompare(b.Path))
  }, [selected])

  const columns: Column<Website>[] = [
    {
      key: 'Name',
      label: t('websites.thName'),
      sortable: true,
      render: (site) => <span className="mono">{site.Name}</span>,
    },
    {
      key: 'Contents',
      label: t('websites.thFiles'),
      sortable: true,
      sortValue: (site) => Object.keys(site.Contents || {}).length,
      render: (site) => <span>{Object.keys(site.Contents || {}).length}</span>,
    },
    {
      key: 'Size',
      label: t('websites.thSize'),
      sortable: true,
      sortValue: siteSize,
      render: (site) => <span className="mono dim">{formatSize(siteSize(site))}</span>,
    },
  ]

  const openAdd = () => {
    setEditing(null)
    setShowAdd(true)
  }

  const openEdit = (content: WebContent) => {
    setEditing(content)
    setShowAdd(true)
  }

  const openView = (content: WebContent) => {
    setViewing(content)
    if (content.DataB64) {
      setViewText(b64ToText(content.DataB64))
    } else {
      setViewText(t('websites.noContentData'))
    }
  }

  const saveContent = async (path: string, contentType: string, fileDataB64: string, text: string) => {
    if (!selected) return
    setBusy(true)
    try {
      const body = {
        path,
        content_type: contentType || undefined,
        file_data_b64: fileDataB64 || undefined,
        text: fileDataB64 ? undefined : text,
      }
      const exists = Boolean(selected.Contents[path])
      const updated = exists
        ? await api.websiteUpdateContent(selected.Name, body)
        : await api.websiteAddContent(selected.Name, body)
      setSelected(updated)
      setShowAdd(false)
      await load()
      toast.push('success', t('websites.contentSaved'))
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!removing) return
    setBusy(true)
    try {
      if (removing.kind === 'site') {
        await api.websiteRemove(removing.siteName)
        toast.push('success', t('websites.removed', { name: removing.siteName }))
        setSelected(null)
        load()
      } else if (removing.content) {
        const updated = await api.websiteRemoveContent(removing.siteName, [removing.content.Path])
        setSelected(updated)
        toast.push('success', t('websites.contentRemoved', { path: removing.content.Path }))
      }
      setRemoving(null)
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const downloadContent = (content: WebContent) => {
    if (!content.DataB64) return
    const bytes = b64ToUint8(content.DataB64)
    const blob = new Blob([bytes.buffer as ArrayBuffer], { type: content.ContentType || 'application/octet-stream' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = content.Path.split('/').pop() || content.Path
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('websites.title')}</div>
          <div className="page-sub">{t('websites.sub', { count: websites.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {!loading && websites.length === 0 && !error ? (
        <div className="empty">{t('websites.empty')}</div>
      ) : (
        <div className="card card-flush">
          <DataTable
            columns={columns}
            rows={websites}
            rowKey={(site) => site.Name}
            searchable
            searchPlaceholder={t('websites.search')}
            searchText={(site) => site.Name}
            loading={loading}
            empty={t('websites.empty')}
            onRowClick={selectSite}
          />
        </div>
      )}

      {selected && (
        <div className="card" style={{ marginTop: 14 }}>
          <div className="card-row">
            <div className="card-title">
              <span className="mono">{selected.Name}</span>
              <span className="dim" style={{ marginLeft: 10, fontWeight: 400 }}>
                {detailLoading ? '…' : `${t('websites.thFiles')}: ${contentList.length} · ${formatSize(siteSize(selected))}`}
              </span>
            </div>
            <div className="toolbar">
              <button type="button" className="btn" onClick={openAdd} disabled={detailLoading}>
                {t('websites.addContent')}
              </button>
              <button type="button" className="btn danger" onClick={() => setRemoving({ kind: 'site', siteName: selected.Name })}>
                {t('websites.removeSite')}
              </button>
            </div>
          </div>
          {contentList.length === 0 ? (
            <div className="empty">{t('websites.noContent')}</div>
          ) : (
            <table className="data">
              <thead>
                <tr>
                  <th>{t('websites.thPath')}</th>
                  <th>{t('websites.thType')}</th>
                  <th>{t('websites.thSize')}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {contentList.map((c) => (
                  <tr key={c.Path}>
                    <td className="mono">{c.Path}</td>
                    <td className="mono dim">{c.ContentType || '—'}</td>
                    <td className="mono dim">{formatSize(c.Size)}</td>
                    <td>
                      <div className="toolbar" style={{ justifyContent: 'flex-end' }}>
                        <button type="button" className="btn sm" onClick={() => openView(c)}>
                          {t('websites.view')}
                        </button>
                        <button type="button" className="btn sm" onClick={() => downloadContent(c)} disabled={!c.DataB64}>
                          {t('websites.download')}
                        </button>
                        <button type="button" className="btn sm" onClick={() => openEdit(c)}>
                          {t('websites.edit')}
                        </button>
                        <button type="button"
                          className="btn sm danger"
                          onClick={() => setRemoving({ kind: 'content', siteName: selected.Name, content: c })}
                        >
                          {t('websites.removeContent')}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      <ContentEditor
        open={showAdd}
        siteName={selected?.Name || ''}
        existing={editing}
        onClose={() => setShowAdd(false)}
        onSave={saveContent}
        busy={busy}
      />

      <DetailDrawer
        open={!!viewing}
        title={t('websites.viewContent')}
        subtitle={viewing ? `${selected?.Name || ''} ${viewing.Path}` : undefined}
        onClose={() => setViewing(null)}
        width={640}
      >
        <div className="viewer">
          <pre className="viewer-pre mono">{viewText}</pre>
        </div>
      </DetailDrawer>

      <ConfirmDialog
        open={!!removing}
        title={removing?.kind === 'site' ? t('websites.confirmRemove') : t('websites.confirmRemoveContent')}
        danger
        busy={busy}
        confirmLabel={t('common.confirm')}
        onConfirm={remove}
        onCancel={() => setRemoving(null)}
      >
        <p>
          {removing?.kind === 'site'
            ? t('websites.confirmRemoveBody', { name: removing.siteName })
            : removing?.content
              ? t('websites.confirmRemoveContentBody', { path: removing.content.Path })
              : ''}
        </p>
      </ConfirmDialog>
    </div>
  )
}

function ContentEditor(props: {
  open: boolean
  siteName: string
  existing: WebContent | null
  onClose: () => void
  onSave: (path: string, contentType: string, fileDataB64: string, text: string) => void
  busy: boolean
}) {
  const { t } = useTranslation()
  const [path, setPath] = useState('')
  const [contentType, setContentType] = useState('')
  const [text, setText] = useState('')
  const [fileName, setFileName] = useState('')
  const [fileB64, setFileB64] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (props.open) {
      setPath(props.existing?.Path || '')
      setContentType(props.existing?.ContentType || '')
      setText(props.existing?.DataB64 ? b64ToText(props.existing.DataB64) : '')
      setFileName('')
      setFileB64('')
    }
  }, [props.open, props.existing])

  const pickFile = (f: File | undefined) => {
    if (!f) return
    setFileName(f.name)
    const r = new FileReader()
    r.onload = () => setFileB64(String(r.result).split(',')[1] || '')
    r.readAsDataURL(f)
  }

  const submit = () => {
    if (!path) return
    props.onSave(path.trim(), contentType.trim(), fileB64, text)
  }

  return (
    <DetailDrawer
      open={props.open}
      title={props.existing ? t('websites.editContent') : t('websites.addContent')}
      subtitle={props.siteName}
      onClose={props.onClose}
      footer={
        <div className="toolbar" style={{ justifyContent: 'flex-end' }}>
          <button type="button" className="btn" onClick={props.onClose}>
            {t('common.cancel')}
          </button>
          <button type="button" className="btn primary" onClick={submit} disabled={props.busy || !path}>
            {props.busy ? t('common.working') : t('common.save')}
          </button>
        </div>
      }
    >
      <div className="form">
        <label className="field">
          <span>{t('websites.pathLabel')}</span>
          <input
            className="input"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder={t('websites.pathPlaceholder')}
          />
        </label>
        <label className="field">
          <span>{t('websites.typeLabel')}</span>
          <input
            className="input"
            value={contentType}
            onChange={(e) => setContentType(e.target.value)}
            placeholder={t('websites.typePlaceholder')}
          />
        </label>
        <div className="field">
          <span>{t('websites.contentLabel')}</span>
          <textarea
            className="input area"
            rows={8}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={t('websites.textPlaceholder')}
          />
        </div>
        <div className="field">
          <span>{t('websites.orUpload')}</span>
          <input
            ref={fileRef}
            type="file"
            className="file"
            onChange={(e) => pickFile(e.target.files?.[0])}
          />
          {fileName && (
            <div className="file-name mono">
              {fileName} {fileB64 && `(${formatSize((fileB64.length * 3) / 4)})`}
            </div>
          )}
        </div>
      </div>
    </DetailDrawer>
  )
}

function formatSize(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = bytes
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

function b64ToUint8(b64: string): Uint8Array {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes
}

function b64ToText(b64: string): string {
  try {
    return new TextDecoder().decode(b64ToUint8(b64))
  } catch {
    return ''
  }
}
