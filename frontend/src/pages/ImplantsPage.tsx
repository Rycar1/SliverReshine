import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { base64ToBytes, triggerDownload } from '../lib/binary'
import { parseC2Url } from '../lib/c2'
import type { CompilerInfo, ImplantBuild, ImplantConfig, ImplantProfile, Job, Session } from '../lib/types'
import ConfirmDialog from '../components/common/ConfirmDialog'
import { useToast } from '../components/common/Toast'
import './pages.css'

const OS_ARCHES: { os: string; arches: string[] }[] = [
  { os: 'windows', arches: ['amd64', '386', 'arm64'] },
  { os: 'linux', arches: ['amd64', '386', 'arm64', 'arm'] },
  { os: 'darwin', arches: ['amd64', 'arm64'] },
  { os: 'freebsd', arches: ['amd64', '386', 'arm64', 'arm'] },
]

// Transports the build form can generate.
//
// `tcp-pivot` is the multi-hop transport: the implant connects to a pivot
// listener running on another implant, which relays it to the server. Without
// it here the only way to build a pivot implant was the raw API with a
// fully-qualified tcppivot:// URL, because the protocol dropdown had no entry
// for it — which is exactly the gap that made chained pivots awkward.
const C2_TYPES = ['mtls', 'http', 'https', 'dns', 'wireguard', 'bind', 'tcp-pivot']

// Display labels for the transport dropdown. The values stay the protocol names
// the API expects; the labels say what each transport is for. tcp-pivot needs
// the gloss in particular — it is the only multi-hop option in the list, and the
// bare name gives no hint that it is how you chain through another implant.
const C2_LABELS: Record<string, string> = {
  mtls: 'mTLS',
  http: 'HTTP',
  https: 'HTTPS',
  dns: 'DNS',
  wireguard: 'WireGuard',
  // The listen/forward transport. The label leads with "Bind" because that is
  // the word the listener page and the session's ActiveC2 field use, so an
  // operator who built one here recognises it there. "Listen" is the gloss
  // because it is what the transport actually does, and because "bind" on its
  // own reads like the socket call rather than a direction of connection.
  bind: 'Bind (listen / forward)',
  'tcp-pivot': 'TCP Pivot (multi-hop)',
}

// Per-OS output formats, mirroring sliver-client's format options. `service`
// is Windows-only; `shellcode` is Windows/Linux/macOS; `shared` applies to
// every OS. Sending an unsupported format to the server would either fail or
// get silently forced back to exe, so the dropdown only offers what the
// target OS actually supports.
const FORMATS: Record<string, string[]> = {
  windows: ['exe', 'service', 'shellcode', 'shared'],
  linux: ['exe', 'shared', 'shellcode'],
  darwin: ['exe', 'shared', 'shellcode'],
  freebsd: ['exe', 'shared'],
}

/**
 * Architectures each OS can produce shellcode for.
 *
 * Sliver enforces this per platform and the three rules differ, so a per-OS
 * format list is not enough: it offered "windows/arm64 shellcode", which the
 * server then refused with "windows shellcode format is only supported for
 * amd64 and 386 architectures" -- after the operator had waited for a build
 * that was never possible.
 *
 * An OS with no entry produces no shellcode at all.
 */
const SHELLCODE_ARCHES: Record<string, string[]> = {
  windows: ['amd64', '386'],
  linux: ['amd64', 'arm64'],
  darwin: ['arm64'],
}

/** The formats actually buildable for one OS/arch pair. */
function formatsFor(os: string, arch: string): string[] {
  const base = FORMATS[os] || ['exe']
  return base.filter((f) => {
    if (f !== 'shellcode') return true
    const arches = SHELLCODE_ARCHES[os]
    return !!arches && arches.includes(arch)
  })
}

// Sliver names the WireGuard listener job "wg" while the build form calls the
// transport "wireguard"; the other transports share their job name.
function listenerJobName(type: string): string {
  return type === 'wireguard' ? 'wg' : type
}

export default function ImplantsPage() {
  const [builds, setBuilds] = useState<ImplantBuild[]>([])
  const [profiles, setProfiles] = useState<ImplantProfile[]>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [generating, setGenerating] = useState(false)
  const [profileName, setProfileName] = useState('')

  const [name, setName] = useState('my-implant')
  const [os, setOs] = useState('windows')
  const [arch, setArch] = useState('amd64')
  const [format, setFormat] = useState('exe')
  const [c2Type, setC2Type] = useState('mtls')
  const [c2Host, setC2Host] = useState('127.0.0.1')
  const [c2Port, setC2Port] = useState('8888')
  const [reconInterval, setReconInterval] = useState('60')
  const [jitter, setJitter] = useState('20')
  const [obfuscate, setObfuscate] = useState(true)
  const [debug, setDebug] = useState(false)
  const [evasion, setEvasion] = useState(false)
  const [deleting, setDeleting] = useState<{ kind: 'build' | 'profile'; name: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [compiler, setCompiler] = useState<CompilerInfo | null>(null)
  const [sessions, setSessions] = useState<Session[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const { t } = useTranslation()
  const toast = useToast()

  const osSummary = useMemo(() => {
    const m = new Map<string, number>()
    for (const b of builds) {
      const key = b.OS || 'unknown'
      m.set(key, (m.get(key) || 0) + 1)
    }
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [builds])

  const load = async () => {
    try {
      const data = await api.builders()
      setBuilds(data.builders || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const loadProfiles = async () => {
    try {
      const data = await api.implantProfiles()
      setProfiles(data.profiles || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const loadCompiler = async () => {
    try {
      setCompiler(await api.compiler())
    } catch {
      setCompiler(null)
    }
  }

  const loadDetect = async () => {
    try {
      const [s, j] = await Promise.all([api.sessions(), api.jobs()])
      setSessions(s.sessions || [])
      setJobs(j.jobs || [])
    } catch {
      // best-effort: keep the previous detection when the server is unreachable
    }
  }

  useEffect(() => {
    load()
    loadProfiles()
    loadCompiler()
    loadDetect()
    const t = setInterval(load, 3000)
    const td = setInterval(loadDetect, 5000)
    return () => {
      clearInterval(t)
      clearInterval(td)
    }
  }, [])

  const detectedC2 = useMemo(() => {
    const session = sessions.find((s) => s.ActiveC2)
    const host = session ? parseC2Url(session.ActiveC2).host : ''
    const typeName = listenerJobName(c2Type)
    const match = jobs.find((j) => j.Name.toLowerCase() === typeName)
    const fallback = jobs[0]
    const useJob = match || fallback
    return {
      host,
      port: match ? String(match.Port) : '',
      anyPort: useJob ? String(useJob.Port) : '',
      source: useJob?.Name || '',
      match: !!match,
    }
  }, [sessions, jobs, c2Type])

  useEffect(() => {
    if (c2Host === '127.0.0.1' && detectedC2.host) setC2Host(detectedC2.host)
    if (c2Port === '8888' && detectedC2.port) setC2Port(detectedC2.port)
  }, [detectedC2, c2Host, c2Port])

  const currentOS = OS_ARCHES.find((o) => o.os === os)
  const formatOptions = formatsFor(os, arch)

  const formatLabel = (f: string) =>
    f === 'exe'
      ? 'EXE'
      : f === 'service'
        ? t('implants.formatService')
        : f === 'shellcode'
          ? t('implants.formatShellcode')
          : t('implants.formatShared')

  const buildConfig = (): Partial<ImplantConfig> => ({
    name,
    os,
    arch,
    format,
    target: os,
    transport: c2Type,
    c2: [{ address: `${c2Host}:${c2Port}`, protocol: c2Type }],
    mtls: c2Type === 'mtls',
    http: c2Type === 'http' || c2Type === 'https',
    dns: c2Type === 'dns',
    wireguard: c2Type === 'wireguard',
    interval: Number(reconInterval),
    jitter: Number(jitter),
    debug,
    evasion,
    obfuscate,
    maxConnectionErrors: 1000,
    limitDomainJoined: false,
  })

  const saveProfile = async () => {
    if (!profileName) return
    setMessage('')
    try {
      await api.saveImplantProfile({ name: profileName, is_beacon: false, config: buildConfig() })
      setMessage(t('profiles.saved', { name: profileName }))
      setProfileName('')
      loadProfiles()
    } catch (e) {
      setMessage(t('profiles.failed', { msg: (e as Error).message }))
    }
  }

  const deleteProfile = async (p: ImplantProfile) => {
    setBusy(true)
    try {
      await api.deleteImplantProfile(p.Name)
      setDeleting(null)
      toast.push('success', t('profiles.deleted', { name: p.Name }))
      loadProfiles()
    } catch (e) {
      toast.push('error', t('profiles.failed', { msg: (e as Error).message }))
    } finally {
      setBusy(false)
    }
  }

  // 复用:将 profile 的配置载入生成表单
  const loadProfile = (p: ImplantProfile) => {
    const cfg = p.Config
    if (!cfg) return
    setName(p.Name)
    if (cfg.OS) setOs(cfg.OS)
    if (cfg.Arch) setArch(cfg.Arch)
    if (cfg.Format) setFormat(cfg.Format)
    const c2 = cfg.C2?.[0]
    if (c2?.URL) {
      const { proto, host, port } = parseC2Url(c2.URL)
      setC2Type(proto)
      if (host) setC2Host(host)
      if (port) setC2Port(port)
    }
    if (cfg.Interval) setReconInterval(String(cfg.Interval))
    if (cfg.Jitter !== undefined) setJitter(String(cfg.Jitter))
    if (cfg.Obfuscate !== undefined) setObfuscate(cfg.Obfuscate)
    if (cfg.Debug !== undefined) setDebug(cfg.Debug)
    if (cfg.Evasion !== undefined) setEvasion(cfg.Evasion)
    setMessage(t('profiles.loaded', { name: p.Name }))
  }

  const generate = async () => {
    setGenerating(true)
    setMessage('')
    try {
      const cfg = buildConfig()
      const res = await api.generate(cfg)
      if (res.success && res.data) {
        triggerDownload(res.name || name, base64ToBytes(res.data))
      }
      setMessage(
        res.success ? t('implants.generated', { msg: res.message }) : t('implants.failed', { msg: res.message }),
      )
      load()
    } catch (e) {
      setMessage(t('implants.failed', { msg: (e as Error).message }))
    } finally {
      setGenerating(false)
    }
  }

  const downloadBuild = async (b: ImplantBuild) => {
    setMessage('')
    try {
      const res = await api.regenerate(b.Name)
      if (res.data) {
        triggerDownload(res.name || b.Name, base64ToBytes(res.data))
      } else {
        setMessage(t('implants.failed', { msg: 'no file data returned' }))
      }
    } catch (e) {
      setMessage(t('implants.failed', { msg: (e as Error).message }))
    }
  }

  const regenerateBuild = async (b: ImplantBuild) => {
    setMessage('')
    try {
      const res = await api.regenerate(b.Name)
      setMessage(
        res.success ? t('implants.regenerated', { name: b.Name }) : t('implants.failed', { msg: res.message }),
      )
      load()
    } catch (e) {
      setMessage(t('implants.failed', { msg: (e as Error).message }))
    }
  }

  const deleteBuild = async (b: ImplantBuild) => {
    setBusy(true)
    try {
      await api.deleteImplantBuild(b.Name)
      setDeleting(null)
      toast.push('success', t('implants.deleted', { name: b.Name }))
      load()
    } catch (e) {
      toast.push('error', t('implants.failed', { msg: (e as Error).message }))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('implants.title')}</div>
          <div className="page-sub">{t('implants.sub', { count: builds.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
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

      {compiler && (
        <div className="compiler-strip">
          <span className="compiler-tag">
            {t('implants.compiler')}: {compiler.GOOS}/{compiler.GOARCH}
          </span>
          <span className="compiler-tag mono">{compiler.Targets?.length || 0} targets</span>
          <span className="compiler-tag mono">{compiler.CrossCompilers?.length || 0} cross-compilers</span>
          {compiler.CrossCompilers?.map((cc) => (
            <span key={`${cc.TargetGOOS}/${cc.TargetGOARCH}`} className="compiler-tag mono">
              {cc.TargetGOOS}/{cc.TargetGOARCH}
            </span>
          ))}
        </div>
      )}

      {builds.length > 0 && (
        <div className="dash-stats build-summary">
          <button type="button" className="dash-stat" onClick={() => {}}>
            <div className="dash-stat-value mono">{builds.length}</div>
            <div className="dash-stat-label">{t('implants.totalBuilds')}</div>
          </button>
          {osSummary.map(([osName, count]) => (
            <button type="button" key={osName} className="dash-stat" onClick={() => {}}>
              <div className="dash-stat-value mono">{count}</div>
              <div className="dash-stat-label">{osName}</div>
            </button>
          ))}
        </div>
      )}

      <div className="card">
        <div className="card-title">{t('implants.genTitle')}</div>
        <div className="form-grid">
          <div className="field">
            <label>{t('implants.name')}</label>
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('implants.os')}</label>
            <select
              value={os}
              onChange={(e) => {
                const next = e.target.value
                setOs(next)
                setArch(OS_ARCHES.find((o) => o.os === next)?.arches[0] || 'amd64')
                const formats = formatsFor(next, arch)
                if (!formats.includes(format)) setFormat(formats[0])
              }}
            >
              {OS_ARCHES.map((o) => (
                <option key={o.os} value={o.os}>
                  {o.os}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('implants.arch')}</label>
              <select
                value={arch}
                onChange={(e) => {
                  // The format has to follow the arch, not just the OS: shellcode
                  // is offered for windows, linux and darwin but only builds on
                  // some architectures of each, so switching arch must not leave
                  // an unbuildable pair selected.
                  const next = e.target.value
                  setArch(next)
                  const allowed = formatsFor(os, next)
                  if (!allowed.includes(format)) setFormat(allowed[0])
                }}
              >
              {currentOS?.arches.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('implants.format')}</label>
            <select value={format} onChange={(e) => setFormat(e.target.value)}>
              {formatOptions.map((f) => (
                <option key={f} value={f}>
                  {formatLabel(f)}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('implants.c2Type')}</label>
            <select value={c2Type} onChange={(e) => setC2Type(e.target.value)}>
              {C2_TYPES.map((c) => (
                <option key={c} value={c}>
                  {C2_LABELS[c] ?? c}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label>{t('implants.c2Host')}</label>
            <input value={c2Host} onChange={(e) => setC2Host(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('implants.c2Port')}</label>
            <input value={c2Port} onChange={(e) => setC2Port(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('implants.interval')}</label>
            <input type="number" value={reconInterval} onChange={(e) => setReconInterval(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('implants.jitter')}</label>
            <input type="number" value={jitter} onChange={(e) => setJitter(e.target.value)} />
          </div>
          <div className="field">
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <input type="checkbox" checked={obfuscate} onChange={(e) => setObfuscate(e.target.checked)} />
              {t('implants.obfuscate')}
            </label>
          </div>
          <div className="field">
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <input type="checkbox" checked={debug} onChange={(e) => setDebug(e.target.checked)} />
              {t('implants.debug')}
            </label>
          </div>
          <div className="field">
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <input type="checkbox" checked={evasion} onChange={(e) => setEvasion(e.target.checked)} />
              {t('implants.evasion')}
            </label>
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={generate} disabled={generating}>
              {generating ? t('implants.generating') : t('implants.generate')}
            </button>
          </div>
        </div>
        {detectedC2.host && detectedC2.anyPort && (
          <div
            className="form-hint"
            style={{ marginTop: 8, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}
          >
            <span>
              {detectedC2.match
                ? t('implants.detectedListener', {
                    name: detectedC2.source,
                    host: detectedC2.host,
                    port: detectedC2.port,
                  })
                : t('implants.detectedOther', {
                    type: c2Type,
                    name: detectedC2.source,
                    host: detectedC2.host,
                    port: detectedC2.anyPort,
                  })}
            </span>
            <button
              type="button"
              className="btn sm"
              onClick={() => {
                setC2Host(detectedC2.host)
                setC2Port(detectedC2.anyPort)
              }}
            >
              {t('common.use')}
            </button>
          </div>
        )}
        <div className="profile-save" style={{ marginTop: 4, display: 'flex', gap: 8, alignItems: 'center' }}>
          <input
            className="profile-name-input"
            type="text"
            placeholder={t('profiles.profileName')}
            value={profileName}
            onChange={(e) => setProfileName(e.target.value)}
            style={{
              padding: '6px 10px',
              borderRadius: 'var(--radius)',
              border: '1px solid var(--border-strong)',
              background: 'var(--surface-soft)',
              color: 'var(--text)',
              fontSize: 'var(--fs-base)',
              flex: 1,
              maxWidth: 320,
            }}
          />
          <button type="button" className="btn" onClick={saveProfile} disabled={!profileName}>
            {t('profiles.saveAs')}
          </button>
        </div>
      </div>

      {profiles.length > 0 && (
        <div className="card card-flush">
          <div className="card-title" style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)', margin: 0 }}>
            {t('profiles.title')}
          </div>
          <table className="data">
            <thead>
              <tr>
                <th>{t('profiles.thName')}</th>
                <th>{t('profiles.thOsArch')}</th>
                <th>{t('profiles.thFormat')}</th>
                <th>{t('profiles.thC2')}</th>
                <th>{t('profiles.thInterval')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {profiles.map((p) => (
                <tr key={p.Name}>
                  <td className="mono">{p.Name}</td>
                  <td>
                    <span className="badge blue">
                      {p.Config?.OS}/{p.Config?.Arch}
                    </span>
                  </td>
                  <td>{p.Config?.Format || '-'}</td>
                  <td className="mono">{p.Config?.C2?.map((c) => c.URL).join(', ') || '-'}</td>
                  <td className="mono">
                    {p.Config?.Interval}s / {p.Config?.Jitter}%
                  </td>
                  <td>
                    <div className="fs-actions">
                      <button type="button" className="btn sm" onClick={() => loadProfile(p)}>
                        {t('profiles.load')}
                      </button>
                      <button type="button" className="btn sm danger" onClick={() => setDeleting({ kind: 'profile', name: p.Name })}>
                        {t('profiles.delete')}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="card card-flush">
        <table className="data">
          <thead>
            <tr>
              <th>{t('implants.thName')}</th>
              <th>{t('implants.thOsArch')}</th>
              <th>{t('implants.thFormat')}</th>
              <th>{t('implants.thC2')}</th>
              <th>{t('implants.thInterval')}</th>
              <th>{t('implants.thObfuscated')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {builds.length === 0 && (
              <tr>
                <td colSpan={7} className="empty">
                  {t('implants.empty')}
                </td>
              </tr>
            )}
            {builds.map((b) => (
              <tr key={b.Name}>
                <td className="mono">{b.Name}</td>
                <td>
                  <span className="badge blue">
                    {b.OS}/{b.Arch}
                  </span>
                </td>
                <td>{b.ImplantConfig?.Format || '-'}</td>
                <td className="mono">
                  {b.ImplantConfig?.C2?.map((c) => c.URL).join(', ') || '-'}
                </td>
                <td className="mono">
                  {b.ImplantConfig?.Interval}s / {b.ImplantConfig?.Jitter}%
                </td>
                <td>{b.ImplantConfig?.Obfuscate ? t('common.yes') : t('common.no')}</td>
                <td>
                  <div className="fs-actions">
                    <button type="button" className="btn sm" onClick={() => downloadBuild(b)}>
                      {t('implants.download')}
                    </button>
                    <button type="button" className="btn sm" onClick={() => regenerateBuild(b)}>
                      {t('implants.regenerate')}
                    </button>
                    <button type="button" className="btn sm danger" onClick={() => setDeleting({ kind: 'build', name: b.Name })}>
                      {t('profiles.delete')}
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ConfirmDialog
        open={!!deleting}
        title={deleting?.kind === 'build' ? t('implants.confirmDelete') : t('profiles.confirmDelete')}
        danger
        busy={busy}
        confirmLabel={t('profiles.delete')}
        onConfirm={() => {
          if (!deleting) return
          if (deleting.kind === 'build') {
            const b = builds.find((x) => x.Name === deleting.name)
            if (b) deleteBuild(b)
          } else {
            const p = profiles.find((x) => x.Name === deleting.name)
            if (p) deleteProfile(p)
          }
        }}
        onCancel={() => setDeleting(null)}
      >
        <p>{deleting ? t('implants.confirmDeleteBody', { name: deleting.name }) : ''}</p>
      </ConfirmDialog>
    </div>
  )
}
