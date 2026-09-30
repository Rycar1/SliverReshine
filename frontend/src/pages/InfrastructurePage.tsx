import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { CertificateInfo, C2Profile, ShellcodeEncoder, TrafficEncoderReport } from '../lib/types'
import EmptyState from '../components/common/EmptyState'
import { useToast } from '../components/common/Toast'
import './pages.css'

type Section = 'profiles' | 'encoders' | 'certificates'

/**
 * Infrastructure groups the pieces that shape how C2 traffic looks and what
 * material the server has issued: HTTP C2 profiles, the WASM traffic encoders
 * that transform every message, shellcode encoder chains, and the CA/listener
 * certificates. None of these were reachable from the console before.
 */
export default function InfrastructurePage() {
  const { t } = useTranslation()
  const toast = useToast()

  const [section, setSection] = useState<Section>('profiles')

  const [profiles, setProfiles] = useState<C2Profile[]>([])
  const [trafficEncoders, setTrafficEncoders] = useState<string[]>([])
  const [shellcodeEncoders, setShellcodeEncoders] = useState<ShellcodeEncoder[]>([])
  const [caCerts, setCaCerts] = useState<CertificateInfo[]>([])
  const [listenerCerts, setListenerCerts] = useState<CertificateInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // Encoder upload form
  const [encName, setEncName] = useState('')
  const [encWasm, setEncWasm] = useState('')
  const [encReport, setEncReport] = useState<TrafficEncoderReport | null>(null)
  const [uploading, setUploading] = useState(false)

  // Shellcode encoder form
  const [scArch, setScArch] = useState('amd64')
  const [scEncoder, setScEncoder] = useState('')
  const [scPayload, setScPayload] = useState('')
  const [scIterations, setScIterations] = useState(1)
  const [scResult, setScResult] = useState<{ data: string; length: number } | null>(null)

  const load = useCallback(() => {
    Promise.allSettled([
      api.c2Profiles(),
      api.trafficEncoders(),
      api.shellcodeEncoders(),
      api.caCertificates(),
      api.certificates(),
    ])
      .then(([p, te, se, ca, lc]) => {
        if (p.status === 'fulfilled') setProfiles(p.value.profiles || [])
        if (te.status === 'fulfilled') setTrafficEncoders(te.value.encoders || [])
        if (se.status === 'fulfilled') setShellcodeEncoders(se.value.encoders || [])
        if (ca.status === 'fulfilled') setCaCerts(ca.value.certificates || [])
        if (lc.status === 'fulfilled') setListenerCerts(lc.value.certificates || [])
        setError('')
      })
      .catch((e) => setError((e as Error).message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const uploadEncoder = async () => {
    if (!encName || !encWasm) {
      toast.push('error', t('infra.needNameAndWasm'))
      return
    }
    setUploading(true)
    try {
      const report = await api.trafficEncoderAdd(encName.trim(), encWasm.trim())
      setEncReport(report)
      const passed = report.Tests.filter((x) => x.Success).length
      toast.push('success', t('infra.uploaded', { passed, total: report.TotalTests }))
      setEncName('')
      setEncWasm('')
      load()
    } catch (e) {
      toast.push('error', (e as Error).message)
    } finally {
      setUploading(false)
    }
  }

  const removeEncoder = async (name: string) => {
    try {
      await api.trafficEncoderRemove(name)
      toast.push('success', t('infra.removed'))
      load()
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }

  const encodeShellcode = async () => {
    if (!scPayload || !scEncoder) {
      toast.push('error', t('infra.needPayloadAndEncoder'))
      return
    }
    try {
      const res = await api.shellcodeEncode(scEncoder, scArch, scPayload.trim(), scIterations)
      setScResult(res)
      toast.push('success', t('infra.encoded', { n: res.length }))
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }

  const renderCerts = (certs: CertificateInfo[]) =>
    certs.length === 0 ? (
      <EmptyState title={t('infra.noCerts')} subtitle={t('infra.noCertsSub')} />
    ) : (
      <table className="table">
        <thead>
          <tr>
            <th>CN</th>
            <th>{t('infra.keyType')}</th>
            <th>{t('infra.expiry')}</th>
            <th>{t('infra.kind')}</th>
          </tr>
        </thead>
        <tbody>
          {certs.map((c, i) => (
            <tr key={`${c.CN}-${i}`}>
              <td className="mono">{c.CN || '-'}</td>
              <td>{c.KeyType || '-'}</td>
              <td>{c.Expiry || '-'}</td>
              <td>
                <span className={`badge ${c.IsCA ? 'ok' : ''}`}>{c.Certificate || '-'}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    )

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>{t('infra.title')}</h1>
          <p className="muted">{t('infra.sub')}</p>
        </div>
      </div>

      {error && <div className="alert error">{error}</div>}

      <div className="tabs">
        {(['profiles', 'encoders', 'certificates'] as Section[]).map((k) => (
          <button
            key={k}
            className={`tab ${section === k ? 'active' : ''}`}
            onClick={() => setSection(k)}
          >
            {t(`infra.tab.${k}`)}
          </button>
        ))}
      </div>

      {loading ? (
        <p className="muted">{t('common.loading')}</p>
      ) : section === 'profiles' ? (
        <div className="card">
          <h2>{t('infra.profiles')}</h2>
          <p className="muted">{t('infra.profilesSub')}</p>
          {profiles.length === 0 ? (
            <EmptyState title={t('infra.noProfiles')} subtitle={t('infra.noProfilesSub')} />
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>{t('common.name')}</th>
                  <th>User-Agent</th>
                  <th>{t('infra.serverUri')}</th>
                </tr>
              </thead>
              <tbody>
                {profiles.map((p) => (
                  <tr key={p.ID}>
                    <td>{p.Name}</td>
                    <td className="mono small break">{p.UserAgent || '-'}</td>
                    <td className="mono small break">
                      {(p.ServerURI || []).join(', ') || '-'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      ) : section === 'encoders' ? (
        <>
          <div className="card">
            <h2>{t('infra.trafficEncoders')}</h2>
            <p className="muted">{t('infra.trafficEncodersSub')}</p>
            {trafficEncoders.length === 0 ? (
              <p className="muted">{t('infra.noneRegistered')}</p>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('common.name')}</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {trafficEncoders.map((name) => (
                    <tr key={name}>
                      <td className="mono">{name}</td>
                      <td className="right">
                        <button className="btn danger sm" onClick={() => removeEncoder(name)}>
                          {t('common.delete')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="card">
            <h2>{t('infra.uploadEncoder')}</h2>
            <p className="muted">{t('infra.uploadEncoderSub')}</p>
            <label>
              <span>{t('common.name')}</span>
              <input
                className="input"
                value={encName}
                onChange={(e) => setEncName(e.target.value)}
              />
            </label>
            <label>
              <span>WASM (base64)</span>
              <textarea
                className="input mono"
                rows={4}
                value={encWasm}
                onChange={(e) => setEncWasm(e.target.value)}
              />
            </label>
            <button className="btn primary" onClick={uploadEncoder} disabled={uploading}>
              {uploading ? t('infra.testing') : t('infra.uploadAndTest')}
            </button>

            {encReport && (
              <div className="card inner">
                <h3>
                  {t('infra.testReport', {
                    passed: encReport.Tests.filter((x) => x.Success).length,
                    total: encReport.TotalTests,
                  })}
                </h3>
                <table className="table">
                  <tbody>
                    {encReport.Tests.map((x, i) => (
                      <tr key={`${x.Name}-${i}`}>
                        <td>{x.Name}</td>
                        <td>
                          <span className={`badge ${x.Success ? 'ok' : 'bad'}`}>
                            {x.Success ? t('common.pass') : t('common.fail')}
                          </span>
                        </td>
                        <td className="muted small">{x.Err || ''}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          <div className="card">
            <h2>{t('infra.shellcodeEncoders')}</h2>
            <p className="muted">{t('infra.shellcodeEncodersSub')}</p>
            {shellcodeEncoders.length === 0 ? (
              <p className="muted">{t('infra.noneRegistered')}</p>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('infra.arch')}</th>
                    <th>{t('infra.chains')}</th>
                  </tr>
                </thead>
                <tbody>
                  {shellcodeEncoders.map((e) => (
                    <tr key={e.Arch}>
                      <td className="mono">{e.Arch}</td>
                      <td className="mono small break">{e.Encoders.join(' -> ')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <h3>{t('infra.runEncoder')}</h3>
            <div className="grid2">
              <label>
                <span>{t('infra.arch')}</span>
                <input className="input" value={scArch} onChange={(e) => setScArch(e.target.value)} />
              </label>
              <label>
                <span>{t('infra.encoder')}</span>
                <input
                  className="input"
                  placeholder={t('infra.encoderHint')}
                  value={scEncoder}
                  onChange={(e) => setScEncoder(e.target.value)}
                />
              </label>
              <label>
                <span>{t('infra.iterations')}</span>
                <input
                  className="input"
                  type="number"
                  min={1}
                  value={scIterations}
                  onChange={(e) => setScIterations(Number(e.target.value) || 1)}
                />
              </label>
            </div>
            <label>
              <span>{t('infra.rawShellcode')}</span>
              <textarea
                className="input mono"
                rows={3}
                value={scPayload}
                onChange={(e) => setScPayload(e.target.value)}
              />
            </label>
            <button className="btn primary" onClick={encodeShellcode}>
              {t('infra.encode')}
            </button>
            {scResult && (
              <div className="card inner">
                <p className="muted">{t('infra.encoded', { n: scResult.length })}</p>
                <textarea className="input mono" rows={4} readOnly value={scResult.data} />
              </div>
            )}
          </div>
        </>
      ) : (
        <>
          <div className="card">
            <h2>{t('infra.ca')}</h2>
            <p className="muted">{t('infra.caSub')}</p>
            {renderCerts(caCerts)}
          </div>
          <div className="card">
            <h2>{t('infra.listenerCerts')}</h2>
            <p className="muted">{t('infra.listenerCertsSub')}</p>
            {renderCerts(listenerCerts)}
          </div>
        </>
      )}
    </div>
  )
}
