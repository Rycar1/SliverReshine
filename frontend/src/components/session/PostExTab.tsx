import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { DirView } from '../../lib/types'
import { useToast } from '../common/Toast'

type Pane = 'attrs' | 'memfiles' | 'wasm'

const PANES: Pane[] = ['attrs', 'memfiles', 'wasm']

/**
 * PostExTab collects the post-exploitation primitives that previously only
 * existed in the Sliver TUI:
 *
 *  - attrs: chmod / chown / chtimes, including timestomping to keep a file's
 *    original timestamps after it has been touched.
 *  - memfiles: anonymous in-memory files that never hit disk.
 *  - wasm: run WASM extensions in the session.
 */
export default function PostExTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const toast = useToast()

  const [pane, setPane] = useState<Pane>('attrs')


  // --- file attributes ---
  const [attrPath, setAttrPath] = useState('')
  const [attrMode, setAttrMode] = useState('0755')
  const [attrUID, setAttrUID] = useState('0')
  const [attrGID, setAttrGID] = useState('0')
  const [attrRecursive, setAttrRecursive] = useState(false)
  const [atime, setAtime] = useState('')
  const [mtime, setMtime] = useState('')
  const [attrBusy, setAttrBusy] = useState(false)

  // --- memfiles ---
  const [memfiles, setMemfiles] = useState<DirView | null>(null)
  const [memBusy, setMemBusy] = useState(false)

  // --- wasm ---
  const [wasmNames, setWasmNames] = useState<string[]>([])
  const [wasmRegName, setWasmRegName] = useState('')
  const [wasmB64, setWasmB64] = useState('')
  const [wasmRunName, setWasmRunName] = useState('')
  const [wasmArgs, setWasmArgs] = useState('')
  const [wasmOut, setWasmOut] = useState('')

  const loadMemfiles = useCallback(() => {
    api
      .memfiles(sessionId)
      .then(setMemfiles)
      .catch(() => setMemfiles(null))
  }, [sessionId])

  const loadWasm = useCallback(() => {
    api
      .wasmExtensions(sessionId)
      .then((d) => setWasmNames(d.extensions || []))
      .catch(() => setWasmNames([]))
  }, [sessionId])

  const loadPane = useCallback(
    (p: Pane) => {
      if (p === 'memfiles') loadMemfiles()
      if (p === 'wasm') loadWasm()
    },
    [loadMemfiles, loadWasm],
  )

  useEffect(() => {
    loadPane(pane)
  }, [pane, loadPane])

  // --- actions -------------------------------------------------------------



  const runAttr = async (kind: 'chmod' | 'chown' | 'chtimes') => {
    if (!attrPath) {
      toast.push('error', t('postex.needPath'))
      return
    }
    setAttrBusy(true)
    try {
      if (kind === 'chmod') {
        await api.chmod(sessionId, attrPath, attrMode, attrRecursive)
      } else if (kind === 'chown') {
        await api.chown(sessionId, attrPath, attrUID, attrGID, attrRecursive)
      } else {
        // Empty fields mean "now", which is what an operator usually wants when
        // normalising timestamps after a modification.
        const now = Math.floor(Date.now() / 1000)
        await api.chtimes(
          sessionId,
          attrPath,
          atime ? Number(atime) : now,
          mtime ? Number(mtime) : now,
        )
      }
      toast.push('success', t('postex.attrDone'))
    } catch (e) {
      toast.push('error', (e as Error).message)
    } finally {
      setAttrBusy(false)
    }
  }

  const addMemfile = async () => {
    setMemBusy(true)
    try {
      const r = await api.memfilesAdd(sessionId)
      toast.push('success', t('postex.memAdded', { fd: r.fd }))
      loadMemfiles()
    } catch (e) {
      toast.push('error', (e as Error).message)
    } finally {
      setMemBusy(false)
    }
  }

  const removeMemfile = async (fd: number) => {
    try {
      await api.memfilesRemove(sessionId, fd)
      loadMemfiles()
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }


  const registerWasm = async () => {
    if (!wasmRegName || !wasmB64) {
      toast.push('error', t('postex.needWasm'))
      return
    }
    try {
      await api.wasmRegister(sessionId, wasmRegName.trim(), wasmB64.trim())
      toast.push('success', t('postex.wasmRegistered'))
      setWasmRegName('')
      setWasmB64('')
      loadWasm()
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }

  const runWasm = async () => {
    if (!wasmRunName) {
      toast.push('error', t('postex.needWasmName'))
      return
    }
    try {
      const args = wasmArgs.split('\n').map((s) => s.trim()).filter(Boolean)
      const out = await api.wasmExec(sessionId, wasmRunName, args)
      setWasmOut([out.stdout, out.stderr].filter(Boolean).join('\n') || t('postex.noOutput'))
      toast.push('success', t('postex.wasmRan', { code: out.exitCode }))
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }

  return (
    <div className="card card-flush">
      <div className="tabs" style={{ padding: '0 16px' }}>
        {PANES.map((p) => (
          <button
            key={p}
            type="button"
            className={`tab ${pane === p ? 'active' : ''}`}
            onClick={() => setPane(p)}
          >
            {t(`postex.pane.${p}`)}
          </button>
        ))}
      </div>

      <div style={{ padding: '14px 16px' }}>

        {pane === 'attrs' && (
          <>
            <div className="env-form">
              <input
                type="text"
                placeholder={t('postex.path')}
                value={attrPath}
                onChange={(e) => setAttrPath(e.target.value)}
                style={{ flex: 1, minWidth: 220 }}
              />
              <label className="inline-check">
                <input
                  type="checkbox"
                  checked={attrRecursive}
                  onChange={(e) => setAttrRecursive(e.target.checked)}
                />
                {t('postex.recursive')}
              </label>
            </div>
            <div className="env-form">
              <input
                type="text"
                placeholder={t('postex.mode')}
                value={attrMode}
                onChange={(e) => setAttrMode(e.target.value)}
                style={{ width: 90 }}
              />
              <button
                type="button"
                className="btn sm"
                onClick={() => runAttr('chmod')}
                disabled={attrBusy}
              >
                {t('postex.chmod')}
              </button>
              <input
                type="text"
                placeholder="uid"
                value={attrUID}
                onChange={(e) => setAttrUID(e.target.value)}
                style={{ width: 70 }}
              />
              <input
                type="text"
                placeholder="gid"
                value={attrGID}
                onChange={(e) => setAttrGID(e.target.value)}
                style={{ width: 70 }}
              />
              <button
                type="button"
                className="btn sm"
                onClick={() => runAttr('chown')}
                disabled={attrBusy}
              >
                {t('postex.chown')}
              </button>
            </div>
            <div className="env-form">
              <input
                type="text"
                placeholder="atime (unix)"
                value={atime}
                onChange={(e) => setAtime(e.target.value)}
                style={{ width: 130 }}
              />
              <input
                type="text"
                placeholder="mtime (unix)"
                value={mtime}
                onChange={(e) => setMtime(e.target.value)}
                style={{ width: 130 }}
              />
              <button
                type="button"
                className="btn sm"
                onClick={() => runAttr('chtimes')}
                disabled={attrBusy}
              >
                {t('postex.chtimes')}
              </button>
            </div>
            <p className="muted small">{t('postex.attrHint')}</p>
          </>
        )}

        {pane === 'memfiles' && (
          <>
            <div className="env-form">
              <button type="button" className="btn sm primary" onClick={addMemfile} disabled={memBusy}>
                {t('postex.memAdd')}
              </button>
              <button type="button" className="btn sm" onClick={loadMemfiles}>
                {t('common.refresh')}
              </button>
            </div>
            <p className="muted small">{t('postex.memHint')}</p>
          </>
        )}


        {pane === 'wasm' && (
          <>
            <div className="env-form">
              <input
                type="text"
                placeholder={t('postex.wasmName')}
                value={wasmRegName}
                onChange={(e) => setWasmRegName(e.target.value)}
              />
              <textarea
                className="mono"
                rows={2}
                placeholder={t('postex.wasmB64')}
                value={wasmB64}
                onChange={(e) => setWasmB64(e.target.value)}
                style={{ flex: 1, minWidth: 220 }}
              />
              <button type="button" className="btn sm" onClick={registerWasm}>
                {t('postex.wasmRegister')}
              </button>
            </div>
            <div className="env-form">
              <input
                type="text"
                placeholder={t('postex.wasmExtName')}
                value={wasmRunName}
                onChange={(e) => setWasmRunName(e.target.value)}
              />
              <textarea
                className="mono"
                rows={2}
                placeholder={t('postex.wasmArgs')}
                value={wasmArgs}
                onChange={(e) => setWasmArgs(e.target.value)}
                style={{ flex: 1, minWidth: 220 }}
              />
              <button type="button" className="btn sm primary" onClick={runWasm}>
                {t('postex.wasmRun')}
              </button>
            </div>
            <p className="muted small">{t('postex.wasmHint')}</p>
          </>
        )}
      </div>

      {/* --- results --- */}


      {pane === 'memfiles' && (
        memfiles && memfiles.Files && memfiles.Files.length > 0 ? (
          <table className="data">
            <thead>
              <tr>
                <th>{t('common.name')}</th>
                <th>{t('postex.size')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {memfiles.Files.map((f) => (
                <tr key={f.Name}>
                  <td className="mono">{f.Name}</td>
                  <td>{f.Size}</td>
                  <td>
                    <button
                      type="button"
                      className="btn sm danger"
                      onClick={() => removeMemfile(Number(f.Name))}
                    >
                      {t('common.delete')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="empty">{t('postex.noMemfiles')}</div>
        )
      )}


      {pane === 'wasm' && (
        <>
          {wasmNames.length > 0 && (
            <table className="data">
              <thead>
                <tr>
                  <th>{t('postex.wasmExtName')}</th>
                </tr>
              </thead>
              <tbody>
                {wasmNames.map((n) => (
                  <tr key={n}>
                    <td className="mono">{n}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {wasmOut && (
            <pre className="mono small" style={{ padding: '0 16px 14px' }}>
              {wasmOut}
            </pre>
          )}
        </>
      )}
    </div>
  )
}
