import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { ImplantProfile, WebDeliveryFormatInfo, WebDeliveryResult } from '../lib/types'
import { useToast } from '../components/common/Toast'
import './pages.css'

/**
 * WebDelivery publishes a stage and prints the one-line command that fetches and
 * runs it, for targets where a file cannot be uploaded but a command can be run.
 *
 * The stage and the command are produced together on the backend, because the
 * URL in the command has to match the path that was actually published. Building
 * them in two places is how a delivery silently stops working.
 */
export default function WebDeliveryPage() {
  const { t } = useTranslation()
  const toast = useToast()

  const [profiles, setProfiles] = useState<ImplantProfile[]>([])
  const [formats, setFormats] = useState<WebDeliveryFormatInfo[]>([])
  const [profile, setProfile] = useState('')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('8443')
  const [format, setFormat] = useState('psh')
  const [path, setPath] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<WebDeliveryResult | null>(null)

  const load = useCallback(async () => {
    setError('')
    try {
      // Both lists are independent; a failure in one must not stop the other
      // from rendering, so they are settled separately.
      const [p, f] = await Promise.allSettled([
        api.implantProfiles(),
        api.webDeliveryFormats(),
      ])
      if (p.status === 'fulfilled') {
        const list = p.value.profiles || []
        setProfiles(list)
        if (list.length > 0) setProfile((cur) => cur || list[0].Name)
      } else {
        setError((p.reason as Error).message)
      }
      if (f.status === 'fulfilled') setFormats(f.value.formats || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const run = async () => {
    setBusy(true)
    setError('')
    setResult(null)
    try {
      const res = await api.webDelivery({
        profile_name: profile,
        host: host.trim(),
        port: Number(port) || 0,
        format,
        path: path.trim() || undefined,
      })
      setResult(res)
      if (res.warning) toast.push('error', res.warning)
      else toast.push('success', t('webdelivery.ready'))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const copy = async (text: string, label: string) => {
    await navigator.clipboard?.writeText(text)
    toast.push('success', t('topology.copied', { label }))
  }

  const canRun = profile !== '' && host.trim() !== '' && Number(port) > 0 && !busy

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('webdelivery.title')}</div>
          <div className="page-sub">{t('webdelivery.sub')}</div>
        </div>
      </div>

      {error && <div className="card error">{error}</div>}

      <div className="card">
        <div className="card-title">{t('webdelivery.formTitle')}</div>
        <div className="form-grid" style={{ marginBottom: 12 }}>
          <div className="field">
            <label>{t('webdelivery.profile')}</label>
            <select value={profile} onChange={(e) => setProfile(e.target.value)}>
              {profiles.length === 0 && <option value="">{t('webdelivery.noProfiles')}</option>}
              {profiles.map((p) => (
                <option key={p.Name} value={p.Name}>
                  {p.Name}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('webdelivery.host')}</label>
            <input
              value={host}
              onChange={(e) => setHost(e.target.value)}
              placeholder="192.168.1.10"
            />
          </div>
          <div className="field">
            <label>{t('webdelivery.port')}</label>
            <input type="number" value={port} onChange={(e) => setPort(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('webdelivery.format')}</label>
            <select value={format} onChange={(e) => setFormat(e.target.value)}>
              {formats.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.label} ({f.platform})
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('webdelivery.path')}</label>
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="/stage.woff"
            />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={run} disabled={!canRun}>
              {busy ? t('webdelivery.building') : t('webdelivery.build')}
            </button>
          </div>
        </div>
        <p className="muted">{t('webdelivery.hint')}</p>
      </div>

      {result && (
        <div className="card" style={{ marginTop: 14 }}>
          <div className="card-title">{t('webdelivery.resultTitle')}</div>

          {result.warning && <div className="alert error">{result.warning}</div>}

          <div className="field" style={{ marginTop: 8 }}>
            <label>{t('webdelivery.urlLabel')}</label>
            <div className="cred-picker-row">
              <input className="input mono" value={result.url} readOnly />
              <button
                type="button"
                className="btn subtle sm"
                onClick={() => copy(result.url, result.url)}
              >
                {t('webdelivery.copy')}
              </button>
            </div>
          </div>

          <div className="field" style={{ marginTop: 8 }}>
            <label>{t('webdelivery.commandLabel')}</label>
            <div className="cred-picker-row">
              <input className="input mono" value={result.command} readOnly />
              <button
                type="button"
                className="btn subtle sm"
                onClick={() => copy(result.command, t('webdelivery.commandLabel'))}
              >
                {t('webdelivery.copy')}
              </button>
            </div>
          </div>

          <p className="muted">
            {result.job_id > 0
              ? t('webdelivery.jobStarted', { id: result.job_id })
              : t('webdelivery.jobExisting')}
          </p>
        </div>
      )}
    </div>
  )
}
