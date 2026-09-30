import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { NetInterface, SockEntry } from '../../lib/types'

export default function NetworkTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const [ifaces, setIfaces] = useState<NetInterface[]>([])
  const [entries, setEntries] = useState<SockEntry[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [visible, setVisible] = useState(50)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [i, n] = await Promise.all([api.ifconfig(sessionId), api.netstat(sessionId)])
      setIfaces(i.interfaces || [])
      setEntries(n.entries || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    load()
  }, [load])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {error && <div className="error-banner">{error}</div>}
      {loading ? (
        <div className="empty">{t('common.loading')}</div>
      ) : (
        <>
          <div className="card card-flush">
            <div style={{ padding: '12px 16px' }}>
              <div className="card-title" style={{ marginBottom: 0 }}>
                {t('network.interfaces')}
              </div>
            </div>
            {ifaces.length === 0 ? (
              <div className="empty">{t('network.empty')}</div>
            ) : (
              <table className="data">
                <thead>
                  <tr>
                    <th>{t('network.ifaceName')}</th>
                    <th>{t('network.ifaceMac')}</th>
                    <th>{t('network.ifaceIp')}</th>
                  </tr>
                </thead>
                <tbody>
                  {ifaces.map((f, i) => (
                    <tr key={`${f.Name}-${i}`}>
                      <td className="mono">{f.Name}</td>
                      <td className="mono">{f.MAC || '-'}</td>
                      <td className="mono">{f.IPAddresses?.join(', ') || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="card card-flush">
            <div style={{ padding: '12px 16px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div className="card-title" style={{ marginBottom: 0 }}>
                {t('network.connections')}
              </div>
              <button type="button" className="btn sm" onClick={load}>
                {t('network.refresh')}
              </button>
            </div>
            {entries.length === 0 ? (
              <div className="empty">{t('network.empty')}</div>
            ) : (
              <table className="data">
                <thead>
                  <tr>
                    <th>{t('network.proto')}</th>
                    <th>{t('network.local')}</th>
                    <th>{t('network.remote')}</th>
                    <th>{t('network.state')}</th>
                    <th>{t('network.process')}</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.slice(0, visible).map((e, i) => (
                    <tr key={i}>
                      <td className="mono">{e.Protocol}</td>
                      <td className="mono">{`${e.LocalAddr}:${e.LocalPort}`}</td>
                      <td className="mono">{`${e.RemoteAddr}:${e.RemotePort}`}</td>
                      <td>{e.State}</td>
                      <td>{e.ProcessName || '-'}</td>
                    </tr>
                  ))}
                  {entries.length > visible && (
                    <tr>
                      <td colSpan={5} style={{ textAlign: 'center', padding: '10px' }}>
                        <button type="button" className="btn sm" onClick={() => setVisible((v) => v + 50)}>
                          {t('common.loadMore', { count: entries.length - visible })}
                        </button>
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}
    </div>
  )
}
