import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { PivotGraphEntry, PivotListener } from '../../lib/types'

export default function PivotTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const [listeners, setListeners] = useState<PivotListener[]>([])
  const [graph, setGraph] = useState<PivotGraphEntry[] | null>(null)
  const [error, setError] = useState('')
  const [type, setType] = useState('TCP')
  const [bindAddress, setBindAddress] = useState('0.0.0.0:9898')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setError('')
    try {
      const [l, g] = await Promise.all([api.pivotListeners(sessionId), api.pivotGraph()])
      setListeners(l.listeners || [])
      setGraph(g.Children || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [sessionId])

  useEffect(() => {
    load()
  }, [load])

  const startListener = async () => {
    setBusy(true)
    setError('')
    try {
      await api.pivotStartListener(sessionId, type, bindAddress.trim())
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const stopListener = async (id: number) => {
    setError('')
    try {
      await api.pivotStopListener(sessionId, id)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="card">
      <div className="card-title">{t('pivot.title')}</div>
      <p className="page-sub" style={{ marginBottom: 12 }}>
        {t('pivot.hint')}
      </p>
      {error && <div className="error-banner">{error}</div>}

      <div className="card" style={{ margin: '0 0 14px', padding: '12px 14px' }}>
        <div className="card-title" style={{ marginBottom: 10 }}>
          {t('pivot.listeners')}
        </div>
        <div className="pf-form">
          <div className="field">
            <label>{t('pivot.type')}</label>
            <select value={type} onChange={(e) => setType(e.target.value)}>
              <option value="TCP">TCP</option>
              <option value="UDP">UDP</option>
              <option value="NamedPipe">NamedPipe</option>
            </select>
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('pivot.bindAddress')}</label>
            <input value={bindAddress} onChange={(e) => setBindAddress(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={startListener} disabled={busy}>
              {busy ? t('common.working') : t('pivot.start')}
            </button>
          </div>
        </div>
        <table className="data">
          <thead>
            <tr>
              <th>{t('pivot.thId')}</th>
              <th>{t('pivot.thType')}</th>
              <th>{t('pivot.thBind')}</th>
              <th>{t('pivot.thPeers')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {listeners.length === 0 && (
              <tr>
                <td colSpan={5} className="empty">
                  {t('pivot.noListeners')}
                </td>
              </tr>
            )}
            {listeners.map((l) => (
              <tr key={l.ID}>
                <td className="mono">{l.ID}</td>
                <td>{l.Type}</td>
                <td className="mono">{l.BindAddress || '—'}</td>
                <td className="mono">
                  {l.Pivots.length === 0
                    ? '—'
                    : l.Pivots.map((p) => `${p.PeerID}@${p.RemoteAddress}`).join(', ')}
                </td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => stopListener(l.ID)}>
                    {t('pivot.stop')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="card" style={{ margin: 0, padding: '12px 14px' }}>
        <div className="card-title" style={{ marginBottom: 10 }}>
          {t('pivot.graphTitle')}
        </div>
        {graph === null ? (
          <div className="empty">{t('pivot.noGraph')}</div>
        ) : graph.length === 0 ? (
          <div className="empty">{t('pivot.noPeers')}</div>
        ) : (
          <ul className="pivot-tree">
            {graph.map((e) => (
              <PivotNode key={e.PeerID} entry={e} />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function PivotNode({ entry }: { entry: PivotGraphEntry }) {
  const { t } = useTranslation()
  return (
    <li>
      <div className="pivot-node">
        <span className="mono">
          {entry.Name || entry.Hostname || t('pivot.unknownPeer')} ({entry.PeerID})
        </span>
        {entry.SessionID && (
          <span className="mono">
            {' '}· {entry.SessionID}
          </span>
        )}
        {entry.Username && <span className="mono"> · {entry.Username}</span>}
        <span className="mono"> · {entry.OS} {entry.Transport}</span>
        {entry.RemoteAddress && <span className="mono"> · {entry.RemoteAddress}</span>}
      </div>
      {entry.Children.length > 0 && (
        <ul className="pivot-tree">
          {entry.Children.map((c) => (
            <PivotNode key={c.PeerID} entry={c} />
          ))}
        </ul>
      )}
    </li>
  )
}
