import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { bytesToText, triggerDownload } from '../../lib/binary'
import { joinPath, parentOf } from '../../lib/paths'
import type { DirView, GrepOut } from '../../lib/types'
import ConfirmDialog from '../common/ConfirmDialog'
import { useToast } from '../common/Toast'
import '../../pages/pages.css'

function fmtSize(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(1)} MB`
  return `${(size / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function fmtTime(ms: number): string {
  if (!ms) return '-'
  return new Date(ms * 1000).toLocaleString()
}

export default function FilesTab({
  sessionId,
  os,
}: {
  sessionId: string
  os?: string
}) {
  const { t } = useTranslation()
  const toast = useToast()
  const [dir, setDir] = useState<DirView | null>(null)
  const [path, setPath] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const [viewing, setViewing] = useState<{ name: string; data: string } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<{ name: string; isDir: boolean } | null>(null)
  const [recursive, setRecursive] = useState(false)
  const [busy, setBusy] = useState(false)
  const uploadRef = useRef<HTMLInputElement>(null)

  // Content search lives here rather than in Post-Ex: it answers a question
  // about the filesystem, so it belongs beside the browser that shows one. The
  // pattern is matched against file contents on the target, not against the
  // names in the listing.
  const [searchOpen, setSearchOpen] = useState(false)
  const [pattern, setPattern] = useState('')
  const [searchRecursive, setSearchRecursive] = useState(true)
  const [searchBefore, setSearchBefore] = useState(0)
  const [searchAfter, setSearchAfter] = useState(0)
  const [searchOut, setSearchOut] = useState<GrepOut | null>(null)
  const [searching, setSearching] = useState(false)

  const load = useCallback(
    async (p: string) => {
      setLoading(true)
      setError('')
      try {
        const d = await api.fsList(sessionId, p || undefined)
        setDir(d)
        setPath(d.Path)
      } catch (e) {
        setError((e as Error).message)
      } finally {
        setLoading(false)
      }
    },
    [sessionId],
  )

  useEffect(() => {
    load(path)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId])

  const enter = (name: string) => {
    setViewing(null)
    const next = joinPath(path, name, sep)
    load(next)
  }

  const up = () => {
    setViewing(null)
    load(parentOf(path, sep))
  }

  const sep = os === 'windows' ? '\\' : '/'

  const refresh = () => load(path)

  const mkdir = async () => {
    const name = window.prompt(t('files.mkdir'))
    if (!name) return
    try {
      await api.fsMkdir(sessionId, joinPath(path, name, sep))
      setMessage(t('files.refresh'))
      refresh()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const onUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    try {
      const buf = await file.arrayBuffer()
      const b64 = arrayBufferToBase64(buf)
      await api.fsUpload(sessionId, joinPath(path, file.name, sep), b64)
      setMessage(t('files.uploaded', { name: file.name }))
      refresh()
    } catch (err) {
      setError((err as Error).message)
    }
    e.target.value = ''
  }

  const remove = async (name: string) => {
    setBusy(true)
    try {
      await api.fsRm(sessionId, joinPath(path, name, sep), recursive)
      setConfirmDelete(null)
      refresh()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const download = async (name: string, isDir: boolean) => {
    if (isDir) return
    try {
      // The endpoint streams the file, so this is a Blob rather than a base64
      // string: nothing inflates it by a third on the way through.
      const { blob, name: suggested } = await api.fsDownload(sessionId, joinPath(path, name, sep))
      triggerDownload(suggested || name, blob)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const view = async (name: string, isDir: boolean) => {
    if (isDir) {
      enter(name)
      return
    }
    try {
      const res = await api.fsCat(sessionId, joinPath(path, name, sep))
      setViewing({ name: res.Name || name, data: res.Data })
    } catch (e) {
      setError((e as Error).message)
    }
  }

  // Search from wherever the browser currently is: an operator who has navigated
  // into a directory means that directory.
  const runSearch = async () => {
    if (!pattern) {
      toast.push('error', t('files.needPattern'))
      return
    }
    setSearching(true)
    try {
      const out = await api.grep(
        sessionId,
        pattern,
        path || '.',
        searchRecursive,
        searchBefore,
        searchAfter,
      )
      setSearchOut(out)
      const hits = (out.Results || []).reduce((n, f) => n + (f.Matches?.length || 0), 0)
      toast.push('success', t('files.searchDone', { hits }))
    } catch (e) {
      toast.push('error', (e as Error).message)
    } finally {
      setSearching(false)
    }
  }

  // A hit is a file somewhere under the search root. Follow it: point the browser
  // at its directory and open it, so a match turns into the file itself instead
  // of a dead-end line of text.
  const openResult = async (fullPath: string) => {
    const cut = Math.max(fullPath.lastIndexOf('/'), fullPath.lastIndexOf('\\'))
    const dirPath = cut > 0 ? fullPath.slice(0, cut) : fullPath
    const name = cut >= 0 ? fullPath.slice(cut + 1) : fullPath
    await load(dirPath)
    try {
      const res = await api.fsCat(sessionId, fullPath)
      setViewing({ name: res.Name || name, data: res.Data })
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const rename = async (name: string) => {
    const newName = window.prompt(t('files.renamePrompt', { name }), name)
    if (!newName || newName === name) return
    try {
      await api.fsMv(sessionId, joinPath(path, name, sep), joinPath(path, newName, sep))
      setMessage(t('files.renamed', { old: name, new: newName }))
      refresh()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="card card-flush">
      <div style={{ padding: '14px 16px' }}>
        <div className="fs-toolbar">
          <button type="button" className="btn sm" onClick={up} disabled={!path || path === '/'}>
            {t('files.up')}
          </button>
          <button type="button" className="btn sm" onClick={refresh}>
            {t('files.refresh')}
          </button>
          <input
            className="fs-path"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') load(path)
            }}
          />
          <button type="button" className="btn sm" onClick={() => load(path)}>
            {t('files.reload')}
          </button>
          <button type="button" className="btn sm primary" onClick={mkdir}>
            {t('files.mkdir')}
          </button>
          <button type="button" className="btn sm" onClick={() => uploadRef.current?.click()}>
            {t('files.upload')}
          </button>
          <input ref={uploadRef} type="file" hidden onChange={onUpload} />
          <button
            type="button"
            className={`btn sm${searchOpen ? ' primary' : ''}`}
            onClick={() => setSearchOpen((v) => !v)}
          >
            {t('files.search')}
          </button>
        </div>

        {searchOpen && (
          <div className="fs-search">
            <div className="fs-toolbar">
              <input
                type="text"
                placeholder={t('files.pattern')}
                value={pattern}
                onChange={(e) => setPattern(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') runSearch()
                }}
                style={{ flex: 1, minWidth: 200 }}
              />
              <label className="inline-check">
                <input
                  type="checkbox"
                  checked={searchRecursive}
                  onChange={(e) => setSearchRecursive(e.target.checked)}
                />
                {t('files.searchRecursive')}
              </label>
              <label className="inline-check">
                {t('files.before')}
                <input
                  type="number"
                  min={0}
                  value={searchBefore}
                  onChange={(e) => setSearchBefore(Number(e.target.value) || 0)}
                  style={{ width: 56 }}
                />
              </label>
              <label className="inline-check">
                {t('files.after')}
                <input
                  type="number"
                  min={0}
                  value={searchAfter}
                  onChange={(e) => setSearchAfter(Number(e.target.value) || 0)}
                  style={{ width: 56 }}
                />
              </label>
              <button type="button" className="btn sm primary" onClick={runSearch} disabled={searching}>
                {searching ? t('common.working') : t('files.searchRun')}
              </button>
            </div>
            <p className="muted small">
              {t('files.searchHint', { path: path || '.' })}
            </p>
          </div>
        )}
        {error && <div className="error-banner">{error}</div>}
        {message && (
          <div
            className="error-banner"
            style={{
              borderColor: 'var(--green)',
              color: 'var(--green)',
              background: 'var(--success-bg)',
            }}
          >
            {message}
          </div>
        )}
      </div>

      {loading ? (
        <div className="empty">{t('common.loading')}</div>
      ) : !dir || !dir.Files || dir.Files.length === 0 ? (
        <div className="empty">{t('files.empty')}</div>
      ) : (
        <table className="data">
          <thead>
            <tr>
              <th>{t('files.name')}</th>
              <th>{t('files.size')}</th>
              <th>{t('files.type')}</th>
              <th>{t('files.modified')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {dir.Files.map((f, i) => (
              <tr key={`${f.Name}-${i}`}>
                <td className="fs-cell-name mono" onClick={() => view(f.Name, f.IsDir)}>
                  {f.IsDir ? (
                    <span className="dir">
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ verticalAlign: 'middle', marginRight: 5 }}>
                        <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                      </svg>
                      {f.Name}
                    </span>
                  ) : (
                    f.Name
                  )}
                </td>
                <td className="mono">{f.IsDir ? '-' : fmtSize(f.Size)}</td>
                <td>{f.IsDir ? 'dir' : f.Mode || 'file'}</td>
                <td className="mono">{fmtTime(f.ModTime)}</td>
                <td>
                  <div className="fs-actions">
                    <button type="button" className="btn sm" onClick={() => view(f.Name, f.IsDir)}>
                      {f.IsDir ? t('files.path') : t('files.cat')}
                    </button>
                    {!f.IsDir && (
                      <button type="button" className="btn sm" onClick={() => download(f.Name, false)}>
                        {t('files.download')}
                      </button>
                    )}
                    <button type="button" className="btn sm" onClick={() => rename(f.Name)}>
                      {t('files.rename')}
                    </button>
                    <button type="button"
                      className="btn sm danger"
                      onClick={() => {
                        setRecursive(false)
                        setConfirmDelete({ name: f.Name, isDir: f.IsDir })
                      }}
                    >
                      {t('files.delete')}
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {searchOpen && searchOut && (
        <div className="fs-search-results">
          <div className="fs-search-summary muted small">
            {t('files.searchScope', {
              path: searchOut.SearchPath,
              files: (searchOut.Results || []).length,
            })}
          </div>
          {(searchOut.Results || []).length === 0 ? (
            <div className="empty">{t('files.noMatches')}</div>
          ) : (
            <table className="data">
              <thead>
                <tr>
                  <th>{t('files.hitFile')}</th>
                  <th>{t('files.hitMatches')}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {searchOut.Results.map((f) => (
                  <tr key={f.Path}>
                    <td className="mono fs-cell-name" onClick={() => openResult(f.Path)}>
                      {f.Path}
                    </td>
                    <td>
                      {f.IsBinary ? (
                        <span className="badge">{t('files.binary')}</span>
                      ) : (
                        <div className="grep-hits">
                          {(f.Matches || []).map((m, i) => (
                            <div key={i} className="mono small">
                              {(m.LinesBefore || []).map((l, j) => (
                                <div key={`b${j}`} className="muted">
                                  {l}
                                </div>
                              ))}
                              <div>
                                <span className="muted">{m.LineNumber}:</span> {m.Line}
                              </div>
                              {(m.LinesAfter || []).map((l, j) => (
                                <div key={`a${j}`} className="muted">
                                  {l}
                                </div>
                              ))}
                            </div>
                          ))}
                        </div>
                      )}
                    </td>
                    <td>
                      <div className="fs-actions">
                        <button type="button" className="btn sm" onClick={() => openResult(f.Path)}>
                          {t('files.cat')}
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

      {viewing && (
        <div className="file-viewer">
          <div className="file-viewer-header">
            <span className="mono">{viewing.name}</span>
            <span>
              <button type="button" className="btn sm danger" onClick={() => setViewing(null)}>
                {t('files.close')}
              </button>
            </span>
          </div>
          <pre>{bytesToText(viewing.data)}</pre>
        </div>
      )}

      <ConfirmDialog
        open={!!confirmDelete}
        title={t('files.confirmDeleteTitle')}
        danger
        busy={busy}
        confirmLabel={t('files.delete')}
        onConfirm={() => confirmDelete && remove(confirmDelete.name)}
        onCancel={() => setConfirmDelete(null)}
      >
        <p>{confirmDelete ? t('files.confirmDelete', { name: confirmDelete.name }) : ''}</p>
        {confirmDelete?.isDir && (
          <label className="fs-recursive" style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10 }}>
            <input
              type="checkbox"
              checked={recursive}
              onChange={(e) => setRecursive(e.target.checked)}
            />
            <span>{t('files.recursive')}</span>
          </label>
        )}
      </ConfirmDialog>
    </div>
  )
}

function arrayBufferToBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}
