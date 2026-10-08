import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type {
  ImplantProfile,
  WebDeliveryFormatInfo,
  WebDeliveryResult,
  Website,
} from '../lib/types'
import { useToast } from '../components/common/Toast'
import './pages.css'

/**
 * WebDelivery publishes a stage and prints the one-line command that fetches and
 * runs it, for targets where a file cannot be uploaded but a command can be run.
 *
 * The stage and the command are produced together on the backend, because the
 * URL in the command has to match the path that was actually published. Building
 * them in two places is how a delivery silently stops working.
 *
 * The page also lists what is currently published, because a stage that was
 * published is otherwise invisible: the console knows the file is there, but
 * nothing in this view said so, and a delivery that overwrote another looked
 * identical to one that did not.
 */

/**
 * The address the operator reached this console on, used to pre-fill the host.
 *
 * The console runs on the machine the target has to fetch the stage from, so the
 * host in the browser's own URL is the one address already known to route here --
 * a better starting point than an empty box. A loopback or wildcard address says
 * nothing about how a *target* reaches this host, so it is left blank rather than
 * pre-filled with a value that would silently produce a dead command.
 */
function consoleHostFromLocation(): string {
  const h = (window.location.hostname || '').trim()
  if (!h) return ''
  if (h === 'localhost' || h === '0.0.0.0' || h === '::' || h === '[::]') return ''
  if (h === '127.0.0.1' || h === '::1' || h === '[::1]') return ''
  return h
}

interface PublishedRow {
  site: string
  // payload is the stage name taken from the last path segment. Every stage the
  // console publishes is named after the profile it was built from
  // (/stage-linux.woff), and the website is the same for every row, so the
  // basename is the only field that says which payload a row is.
  payload: string
  path: string
  type: string
  size: number
}

/** The last segment of a published path, which is the stage name. */
function payloadName(path: string): string {
  const clean = (path || '').split('?')[0]
  const seg = clean.split('/').filter(Boolean).pop()
  return seg || path || '-'
}

export default function WebDeliveryPage() {
  const { t } = useTranslation()
  const toast = useToast()

  const [profiles, setProfiles] = useState<ImplantProfile[]>([])
  const [formats, setFormats] = useState<WebDeliveryFormatInfo[]>([])
  const [profile, setProfile] = useState('')
  const [host, setHost] = useState(consoleHostFromLocation)
  const [port, setPort] = useState('8443')
  const [format, setFormat] = useState('psh')
  const [path, setPath] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<WebDeliveryResult | null>(null)

  // The published-file inventory. It is kept separate from the form state so a
  // failure to read it cannot stop the form from being usable.
  const [websites, setWebsites] = useState<Website[]>([])
  const [publishedBusy, setPublishedBusy] = useState(false)
  const [publishedError, setPublishedError] = useState('')

  const load = useCallback(async () => {
    setError('')
    try {
      // The lists are independent; a failure in one must not stop the others
      // from rendering, so they are settled separately.
      const [p, f, w] = await Promise.allSettled([
        api.implantProfiles(),
        api.webDeliveryFormats(),
        api.websites(),
      ])
      if (p.status === 'fulfilled') {
        const list = p.value.profiles || []
        setProfiles(list)
        if (list.length > 0) setProfile((cur) => cur || list[0].Name)
      } else {
        setError((p.reason as Error).message)
      }
      if (f.status === 'fulfilled') setFormats(f.value.formats || [])
      if (w.status === 'fulfilled') setWebsites(w.value.websites || [])
      else setPublishedError((w.reason as Error).message)
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const loadPublished = useCallback(async () => {
    setPublishedBusy(true)
    setPublishedError('')
    try {
      const d = await api.websites()
      setWebsites(d.websites || [])
    } catch (e) {
      // Surfaced in the published card rather than thrown away: a list that
      // silently stops updating is worse than one that says it is stale.
      setPublishedError((e as Error).message)
    } finally {
      setPublishedBusy(false)
    }
  }, [])

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
      // The stage was just written, so the inventory is refreshed rather than
      // left showing the state before the build.
      await loadPublished()
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

  const published = useMemo<PublishedRow[]>(() => {
    const rows: PublishedRow[] = []
    for (const site of websites) {
      for (const c of Object.values(site.Contents || {})) {
        rows.push({
          site: site.Name,
          payload: payloadName(c.Path),
          path: c.Path,
          type: c.ContentType,
          size: c.Size,
        })
      }
    }
    return rows.sort(
      (a, b) => a.site.localeCompare(b.site) || a.path.localeCompare(b.path),
    )
  }, [websites])

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
          {result.website && (
            <p className="muted">{t('webdelivery.publishedOn', { name: result.website })}</p>
          )}
        </div>
      )}

      <div className="card" style={{ marginTop: 14 }}>
        <div className="card-header">
          <div className="card-title">{t('webdelivery.publishedTitle')}</div>
          <button
            type="button"
            className="btn subtle sm"
            onClick={loadPublished}
            disabled={publishedBusy}
          >
            {t('webdelivery.refresh')}
          </button>
        </div>

        {publishedError && <div className="alert error">{publishedError}</div>}

        {published.length === 0 ? (
          <div className="empty">{t('webdelivery.publishedEmpty')}</div>
        ) : (
          <table className="data">
            <thead>
              <tr>
                <th>{t('webdelivery.thPayload')}</th>
                <th>{t('webdelivery.thSite')}</th>
                <th>{t('websites.thPath')}</th>
                <th>{t('websites.thType')}</th>
                <th>{t('websites.thSize')}</th>
              </tr>
            </thead>
            <tbody>
              {published.map((r) => (
                <tr key={`${r.site}${r.path}`}>
                  <td className="mono">{r.payload}</td>
                  <td className="mono dim">{r.site}</td>
                  <td className="mono">{r.path}</td>
                  <td className="mono dim">{r.type || '—'}</td>
                  <td className="mono dim">{formatSize(r.size)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

function formatSize(bytes: number): string {
  if (!bytes || bytes < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let n = bytes
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}
