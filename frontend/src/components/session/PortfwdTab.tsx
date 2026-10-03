import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { PortForward, RportFwdListener } from '../../lib/types'
import { useToast } from '../common/Toast'
import StatusBanners from '../common/StatusBanners'

/**
 * Both port-forwarding directions live here, because they are two answers to
 * the same question and an operator picks between them by direction, not by
 * which tab they happen to be in:
 *
 *  - Local: the server listens on a port here, and connections that arrive are
 *    tunnelled *into* the session and dialled from the target. Use it to reach
 *    something the target can see but you cannot.
 *  - Reverse: the implant listens on a port *on the target*, and connections
 *    that arrive there are tunnelled back out and dialled from your machine.
 *    Use it to make a service on your side reachable from the target — and to
 *    catch callbacks that the target itself initiates.
 *
 * The two forms take the same four values in opposite roles, so the labels say
 * which side each address belongs to.
 */
export default function PortfwdTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const toast = useToast()

  // --- local forward: server listens here, dials through the session ---
  const [forwards, setForwards] = useState<PortForward[]>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [localPort, setLocalPort] = useState('8080')
  const [bindAddr, setBindAddr] = useState('127.0.0.1')
  const [remoteHost, setRemoteHost] = useState('127.0.0.1')
  const [remotePort, setRemotePort] = useState('22')

  // --- reverse forward: the implant listens on the target ---
  const [listeners, setListeners] = useState<RportFwdListener[]>([])
  const [rBindAddr, setRBindAddr] = useState('0.0.0.0')
  const [rBindPort, setRBindPort] = useState('')
  const [fwdAddr, setFwdAddr] = useState('127.0.0.1')
  const [fwdPort, setFwdPort] = useState('')

  const load = useCallback(async () => {
    setError('')
    try {
      const d = await api.portfwdList()
      setForwards(d.forwards || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  const loadListeners = useCallback(() => {
    api
      .rportfwd(sessionId)
      .then((d) => setListeners(d.listeners || []))
      .catch(() => setListeners([]))
  }, [sessionId])

  useEffect(() => {
    load()
  }, [load])

  useEffect(() => {
    loadListeners()
  }, [loadListeners])

  const add = async () => {
    setMessage('')
    setError('')
    try {
      const res = await api.portfwdStart({
        session_id: sessionId,
        bind_addr: bindAddr,
        bind_port: Number(localPort) || 0,
        remote_host: remoteHost,
        remote_port: Number(remotePort),
      })
      setMessage(t('portfwd.added', { port: res.localPort ?? localPort }))
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const stop = async (port: number) => {
    setError('')
    try {
      await api.portfwdStop(port)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const startReverse = async () => {
    const bp = Number(rBindPort)
    const fp = Number(fwdPort)
    if (!bp || !fp) {
      toast.push('error', t('portfwd.needPorts'))
      return
    }
    try {
      await api.rportfwdStart(sessionId, rBindAddr || '0.0.0.0', bp, fwdAddr || '127.0.0.1', fp)
      toast.push('success', t('portfwd.reverseStarted'))
      setRBindPort('')
      setFwdPort('')
      loadListeners()
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }

  const stopReverse = async (fwdID: number) => {
    try {
      await api.rportfwdStop(sessionId, fwdID)
      loadListeners()
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }

  return (
    <div className="pf-stack">
      <div className="card">
        <div className="card-title">{t('portfwd.title')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('portfwd.hint')}
        </p>
        <div className="pf-form">
          <div className="field">
            <label>{t('portfwd.localPort')}</label>
            <input type="number" value={localPort} onChange={(e) => setLocalPort(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('portfwd.bindAddr')}</label>
            <input value={bindAddr} onChange={(e) => setBindAddr(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('portfwd.remoteHost')}</label>
            <input value={remoteHost} onChange={(e) => setRemoteHost(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('portfwd.remotePort')}</label>
            <input type="number" value={remotePort} onChange={(e) => setRemotePort(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={add}>
              {t('portfwd.add')}
            </button>
          </div>
        </div>
        <StatusBanners error={error} message={message} />
        <table className="data">
          <thead>
            <tr>
              <th>{t('portfwd.local')}</th>
              <th>{t('portfwd.remote')}</th>
              <th>{t('portfwd.session')}</th>
              <th>{t('portfwd.status')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {forwards.length === 0 && (
              <tr>
                <td colSpan={5} className="empty">
                  {t('portfwd.empty')}
                </td>
              </tr>
            )}
            {forwards.map((f) => (
              <tr key={f.LocalPort}>
                <td className="mono">{`${f.LocalAddr}:${f.LocalPort}`}</td>
                <td className="mono">{`${f.Host}:${f.Port}`}</td>
                <td className="mono">{f.SessionID.slice(0, 12)}</td>
                <td className="mono" title={f.LastConnErr ?? undefined}>
                  {f.LastConnErr ? (
                    <span className="text-danger">{t('portfwd.failing')}</span>
                  ) : (
                    t('portfwd.listening')
                  )}
                </td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => stop(f.LocalPort)}>
                    {t('portfwd.stop')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="card">
        <div className="card-title">{t('portfwd.reverseTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('portfwd.reverseHint')}
        </p>
        <div className="pf-form">
          <div className="field">
            <label>{t('portfwd.bindAddr')}</label>
            <input value={rBindAddr} onChange={(e) => setRBindAddr(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('portfwd.bindPort')}</label>
            <input type="number" value={rBindPort} onChange={(e) => setRBindPort(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('portfwd.forwardAddr')}</label>
            <input value={fwdAddr} onChange={(e) => setFwdAddr(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('portfwd.forwardPort')}</label>
            <input type="number" value={fwdPort} onChange={(e) => setFwdPort(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={startReverse}>
              {t('portfwd.reverseStart')}
            </button>
            <button type="button" className="btn sm" onClick={loadListeners}>
              {t('common.refresh')}
            </button>
          </div>
        </div>
        <table className="data">
          <thead>
            <tr>
              <th>{t('portfwd.id')}</th>
              <th>{t('portfwd.listeningOn')}</th>
              <th>{t('portfwd.dialledHere')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {listeners.length === 0 && (
              <tr>
                <td colSpan={4} className="empty">
                  {t('portfwd.reverseEmpty')}
                </td>
              </tr>
            )}
            {listeners.map((f) => (
              <tr key={f.ID}>
                <td className="mono">{f.ID}</td>
                <td className="mono">{`${f.BindAddress}:${f.BindPort}`}</td>
                <td className="mono">{`${f.ForwardAddress}:${f.ForwardPort}`}</td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => stopReverse(f.ID)}>
                    {t('portfwd.stop')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
