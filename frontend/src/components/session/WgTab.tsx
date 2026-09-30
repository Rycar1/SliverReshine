import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { WGClientConfig, WGTCPForwarder, WGSocksServer } from '../../lib/types'
import DetailDrawer from '../common/DetailDrawer'

export default function WgTab({ sessionId, isWg }: { sessionId: string; isWg: boolean }) {
  const { t } = useTranslation()
  const [forwarders, setForwarders] = useState<WGTCPForwarder[]>([])
  const [socks, setSocks] = useState<WGSocksServer[]>([])
  const [error, setError] = useState('')
  const [localPort, setLocalPort] = useState('1080')
  const [remoteAddr, setRemoteAddr] = useState('10.13.13.1:22')
  const [socksPort, setSocksPort] = useState('1081')
  const [config, setConfig] = useState<WGClientConfig | null>(null)
  const [configBusy, setConfigBusy] = useState(false)
  const [showConfig, setShowConfig] = useState(false)

  const load = useCallback(async () => {
    if (!isWg) return
    setError('')
    try {
      const [f, s] = await Promise.all([api.wgForwarders(sessionId), api.wgSocksServers(sessionId)])
      setForwarders(f.forwarders || [])
      setSocks(s.servers || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [sessionId, isWg])

  useEffect(() => {
    load()
  }, [load])

  const addForwarder = async () => {
    setError('')
    try {
      await api.wgStartPortForward(sessionId, Number(localPort) || 0, remoteAddr.trim())
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const stopForwarder = async (id: number) => {
    setError('')
    try {
      await api.wgStopPortForward(sessionId, id)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const addSocks = async () => {
    setError('')
    try {
      await api.wgStartSocks(sessionId, Number(socksPort) || 0)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const stopSocks = async (id: number) => {
    setError('')
    try {
      await api.wgStopSocks(sessionId, id)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const generateConfig = async () => {
    setConfigBusy(true)
    setError('')
    try {
      const cfg = await api.wgClientConfig()
      setConfig(cfg)
      setShowConfig(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setConfigBusy(false)
    }
  }

  const downloadConfig = () => {
    if (!config) return
    const text = `[Interface]\nPrivateKey = ${config.ClientPrivateKey}\nAddress = ${config.ClientIP}/16\nDNS = 10.13.13.1\n\n[Peer]\nPublicKey = ${config.ServerPubKey}\nAllowedIPs = 0.0.0.0/0\nEndpoint = wg:53`
    const blob = new Blob([text], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'sliver-wg.conf'
    a.click()
    URL.revokeObjectURL(url)
  }

  if (!isWg) {
    return (
      <div className="card">
        <div className="empty">{t('wg.notWgSession')}</div>
      </div>
    )
  }

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">{t('wg.title')}</div>
        <button type="button" className="btn" onClick={generateConfig} disabled={configBusy}>
          {configBusy ? t('common.working') : t('wg.generateConfig')}
        </button>
      </div>
      <p className="page-sub" style={{ marginBottom: 12 }}>
        {t('wg.hint')}
      </p>
      {error && <div className="error-banner">{error}</div>}

      <div className="card" style={{ margin: '0 0 14px', padding: '12px 14px' }}>
        <div className="card-title" style={{ marginBottom: 10 }}>
          {t('wg.forwarders')}
        </div>
        <div className="pf-form">
          <div className="field">
            <label>{t('wg.localPort')}</label>
            <input type="number" value={localPort} onChange={(e) => setLocalPort(e.target.value)} />
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('wg.remoteAddress')}</label>
            <input value={remoteAddr} onChange={(e) => setRemoteAddr(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={addForwarder}>
              {t('wg.addForwarder')}
            </button>
          </div>
        </div>
        <table className="data">
          <thead>
            <tr>
              <th>{t('wg.thId')}</th>
              <th>{t('wg.thLocal')}</th>
              <th>{t('wg.thRemote')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {forwarders.length === 0 && (
              <tr>
                <td colSpan={4} className="empty">
                  {t('wg.noForwarders')}
                </td>
              </tr>
            )}
            {forwarders.map((f) => (
              <tr key={f.ID}>
                <td className="mono">{f.ID}</td>
                <td className="mono">{f.LocalAddr || '—'}</td>
                <td className="mono">{f.RemoteAddr || '—'}</td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => stopForwarder(f.ID)}>
                    {t('wg.stop')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="card" style={{ margin: 0, padding: '12px 14px' }}>
        <div className="card-title" style={{ marginBottom: 10 }}>
          {t('wg.socks')}
        </div>
        <div className="pf-form">
          <div className="field">
            <label>{t('wg.localPort')}</label>
            <input type="number" value={socksPort} onChange={(e) => setSocksPort(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={addSocks}>
              {t('wg.addSocks')}
            </button>
          </div>
        </div>
        <table className="data">
          <thead>
            <tr>
              <th>{t('wg.thId')}</th>
              <th>{t('wg.thLocal')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {socks.length === 0 && (
              <tr>
                <td colSpan={3} className="empty">
                  {t('wg.noSocks')}
                </td>
              </tr>
            )}
            {socks.map((s) => (
              <tr key={s.ID}>
                <td className="mono">{s.ID}</td>
                <td className="mono">{s.LocalAddr || '—'}</td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => stopSocks(s.ID)}>
                    {t('wg.stop')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <DetailDrawer
        open={showConfig}
        title={t('wg.configTitle')}
        subtitle={config?.ClientIP}
        onClose={() => setShowConfig(false)}
        footer={
          <div className="toolbar" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn" onClick={downloadConfig}>
              {t('wg.download')}
            </button>
          </div>
        }
      >
        <div className="viewer">
          <pre className="viewer-pre mono">{config ? formatConfig(config) : ''}</pre>
        </div>
      </DetailDrawer>
    </div>
  )
}

function formatConfig(c: WGClientConfig): string {
  return `[Interface]
PrivateKey = ${c.ClientPrivateKey}
Address = ${c.ClientIP}/16
DNS = 10.13.13.1

[Peer]
PublicKey = ${c.ServerPubKey}
AllowedIPs = 0.0.0.0/0
Endpoint = wg:53`
}
