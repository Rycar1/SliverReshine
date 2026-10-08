import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { bytesToBase64, bytesToText, triggerDownload } from '../../lib/binary'
import { fmtLocalTime, fmtSize } from '../../lib/format'
import { joinPath, parentOf } from '../../lib/paths'
import type { DirView, FileEntry, GrepOut } from '../../lib/types'
import ConfirmDialog from '../common/ConfirmDialog'
import ContextMenu from '../common/ContextMenu'
import StatusBanners from '../common/StatusBanners'
import { useToast } from '../common/Toast'
import FileAttrDialog, { type AttrKind } from './FileAttrDialog'
import '../../pages/pages.css'

// The viewer paints the whole file as one text node, so a big file freezes the
// tab rather than merely loading slowly. Anything larger is a download, not a
// preview -- the same limit the server enforces.
const MAX_VIEW_BYTES = 5 * 1024 * 1024

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
  const [viewing, setViewing] = useState<{
    name: string
    data?: string
    binary?: boolean
    size?: number
  } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<{ name: string; isDir: boolean } | null>(null)
  // The row menu and the attribute dialog it opens. Both are anchored to one
  // listing entry, so the file the operator clicked is the file the change
  // applies to -- no path to retype, no chance of aiming at the wrong one.
  const [menu, setMenu] = useState<{ x: number; y: number; file: FileEntry } | null>(null)
  const [attrTarget, setAttrTarget] = useState<{ file: FileEntry; kind: AttrKind } | null>(null)
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
      const b64 = bytesToBase64(new Uint8Array(buf))
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

  const view = async (name: string, isDir: boolean, size = 0) => {
    if (isDir) {
      enter(name)
      return
    }
    // Refuse before the request: otherwise the target pulls the whole file back
    // only for the viewer to discard it.
    if (size > MAX_VIEW_BYTES) {
      setViewing(null)
      setError(t('files.tooLarge', { size: fmtSize(size), max: fmtSize(MAX_VIEW_BYTES) }))
      return
    }
    try {
      const res = await api.fsCat(sessionId, joinPath(path, name, sep))
      setViewing({ name: res.Name || name, data: res.Data, binary: res.Binary, size: res.Size })
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
  const openResult = async (fullPath: string, isBinary = false) => {
    const cut = Math.max(fullPath.lastIndexOf('/'), fullPath.lastIndexOf('\\'))
    const dirPath = cut > 0 ? fullPath.slice(0, cut) : fullPath
    const name = cut >= 0 ? fullPath.slice(cut + 1) : fullPath
    await load(dirPath)
    // The search already knows this one is binary, so do not ask the target for
    // it just to be told the same thing.
    if (isBinary) {
      setViewing({ name, binary: true })
      return
    }
    try {
      const res = await api.fsCat(sessionId, fullPath)
      setViewing({ name: res.Name || name, data: res.Data, binary: res.Binary, size: res.Size })
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
        <StatusBanners error={error} message={message} />
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
                <td className="fs-cell-name mono" onClick={() => view(f.Name, f.IsDir, f.Size)}>
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
                <td className="mono">{fmtLocalTime(f.ModTime)}</td>
                <td>
                  <div className="fs-actions">
                    <button type="button" className="btn sm" onClick={() => view(f.Name, f.IsDir, f.Size)}>
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
                    <button type="button"
                      className="btn sm"
                      onClick={(e) => {
                        e.stopPropagation()
                        const r = e.currentTarget.getBoundingClientRect()
                        setMenu({ x: r.left, y: r.bottom + 4, file: f })
                      }}
                    >
                      {t('files.more')}
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
                    <td className="mono fs-cell-name" onClick={() => openResult(f.Path, f.IsBinary)}>
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
                        <button type="button" className="btn sm" onClick={() => openResult(f.Path, f.IsBinary)}>
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
          {viewing.binary ? (
            <div className="alert" style={{ margin: 12 }}>{t('files.binaryUnsupported')}</div>
          ) : (
            <pre>{bytesToText(viewing.data)}</pre>
          )}
        </div>
      )}

      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
          items={[
            {
              label: t('files.attrOwner'),
              onSelect: () => setAttrTarget({ file: menu.file, kind: 'owner' }),
            },
            {
              label: t('files.attrPerm'),
              onSelect: () => setAttrTarget({ file: menu.file, kind: 'perm' }),
            },
            {
              label: t('files.attrTime'),
              onSelect: () => setAttrTarget({ file: menu.file, kind: 'time' }),
            },
          ]}
        />
      )}

      {attrTarget && (
        <FileAttrDialog
          sessionId={sessionId}
          os={os}
          path={joinPath(path, attrTarget.file.Name, sep)}
          name={attrTarget.file.Name}
          kind={attrTarget.kind}
          onClose={() => setAttrTarget(null)}
          onDone={() => {
            setAttrTarget(null)
            refresh()
          }}
        />
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
