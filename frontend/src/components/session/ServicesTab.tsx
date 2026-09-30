import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { CallExtensionResult, SSHCommandResult } from '../../lib/types'
import CredentialPicker from '../common/CredentialPicker'

function toBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      resolve(result.split(',')[1] || '')
    }
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}

export default function ServicesTab({ sessionId, os }: { sessionId: string; os: string }) {
  const { t } = useTranslation()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const [svcName, setSvcName] = useState('')
  const [svcDesc, setSvcDesc] = useState('')
  const [svcBinPath, setSvcBinPath] = useState('')
  const [svcHost, setSvcHost] = useState('')
  const [svcArgs, setSvcArgs] = useState('')
  const [svcMsg, setSvcMsg] = useState('')

  const [sshUser, setSshUser] = useState('')
  const [sshHost, setSshHost] = useState('')
  const [sshPort, setSshPort] = useState('22')
  const [sshCmd, setSshCmd] = useState('')
  const [sshPass, setSshPass] = useState('')
  const [sshKey, setSshKey] = useState('')
  const [sshResult, setSshResult] = useState<SSHCommandResult | null>(null)

  const [extNames, setExtNames] = useState<string[]>([])
  const [extName, setExtName] = useState('')
  const [extOs, setExtOs] = useState(os === 'windows' ? 'windows' : os)
  const [extInit, setExtInit] = useState('')
  const [extFile, setExtFile] = useState<File | null>(null)
  const [callName, setCallName] = useState('')
  const [callExport, setCallExport] = useState('')
  const [callArgs, setCallArgs] = useState('')
  const [callServerStore, setCallServerStore] = useState(false)
  const [callResult, setCallResult] = useState<CallExtensionResult | null>(null)

  const loadExtensions = useCallback(async () => {
    try {
      const d = await api.listExtensions(sessionId)
      setExtNames(d.names || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [sessionId])

  useEffect(() => {
    loadExtensions()
  }, [loadExtensions])

  const startService = async () => {
    setBusy(true)
    setError('')
    setSvcMsg('')
    try {
      await api.startService(sessionId, {
        service_name: svcName.trim(),
        description: svcDesc.trim(),
        bin_path: svcBinPath.trim(),
        hostname: svcHost.trim(),
        arguments: svcArgs.trim(),
      })
      setSvcMsg(t('services.startOk'))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const stopService = async (name: string) => {
    setBusy(true)
    setError('')
    setSvcMsg('')
    try {
      await api.stopService(sessionId, name, svcHost.trim())
      setSvcMsg(t('services.stopOk', { name }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const removeService = async (name: string) => {
    setBusy(true)
    setError('')
    setSvcMsg('')
    try {
      await api.removeService(sessionId, name, svcHost.trim())
      setSvcMsg(t('services.removeOk', { name }))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const runSsh = async () => {
    setBusy(true)
    setError('')
    setSshResult(null)
    try {
      const result = await api.runSSHCommand(sessionId, {
        username: sshUser.trim(),
        hostname: sshHost.trim(),
        port: Number(sshPort) || 22,
        command: sshCmd,
        password: sshPass,
        priv_key: sshKey.trim() || undefined,
      })
      setSshResult(result)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const registerExtension = async () => {
    if (!extFile) return
    setBusy(true)
    setError('')
    try {
      const b64 = await toBase64(extFile)
      await api.registerExtension(sessionId, {
        name: extName.trim(),
        os: extOs.trim(),
        init: extInit.trim(),
        data_b64: b64,
      })
      setExtName('')
      setExtFile(null)
      loadExtensions()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const callExtension = async () => {
    setBusy(true)
    setError('')
    setCallResult(null)
    try {
      const result = await api.callExtension(sessionId, {
        name: callName.trim() || extName.trim(),
        export: callExport.trim(),
        server_store: callServerStore,
        args_b64: callArgs.trim() ? btoa(callArgs) : undefined,
      })
      setCallResult(result)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <div className="card">
        <div className="card-title">{t('services.servicesTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('services.servicesHint')}
        </p>
        <div className="pf-form">
          <div className="field">
            <label>{t('services.name')}</label>
            <input value={svcName} onChange={(e) => setSvcName(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.description')}</label>
            <input value={svcDesc} onChange={(e) => setSvcDesc(e.target.value)} />
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('services.binPath')}</label>
            <input value={svcBinPath} onChange={(e) => setSvcBinPath(e.target.value)} placeholder="C:\Windows\System32\evil.exe" />
          </div>
          <div className="field">
            <label>{t('services.hostname')}</label>
            <input value={svcHost} onChange={(e) => setSvcHost(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.arguments')}</label>
            <input value={svcArgs} onChange={(e) => setSvcArgs(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={startService} disabled={busy || !svcName}>
              {busy ? t('common.working') : t('services.start')}
            </button>
          </div>
        </div>
        {svcMsg && (
          <div className="success-banner">
            {svcMsg}
          </div>
        )}
        <div className="toolbar" style={{ marginTop: 8 }}>
          {os === 'windows' && (
            <>
              <button type="button" className="btn sm" onClick={() => stopService(svcName)} disabled={!svcName}>
                {t('services.stop')}
              </button>
              <button type="button" className="btn sm danger" onClick={() => removeService(svcName)} disabled={!svcName}>
                {t('services.remove')}
              </button>
            </>
          )}
        </div>
      </div>

      <div className="card">
        <div className="card-title">{t('services.sshTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('services.sshHint')}
        </p>
        {/* SSH authenticates with a password or a key. The vault only holds
            passwords, so picking one fills the username and password and leaves
            the key field alone for the operator to fill if that is the method. */}
        <div className="field">
          <CredentialPicker
            requirePlaintext
            onChange={(c) => {
              if (!c) return
              setSshUser(c.username)
              setSshPass(c.secret)
            }}
          />
        </div>
        <div className="pf-form">
          <div className="field">
            <label>{t('services.sshUsername')}</label>
            <input value={sshUser} onChange={(e) => setSshUser(e.target.value)} placeholder="root" />
          </div>
          <div className="field" style={{ flex: 2 }}>
            <label>{t('services.sshHostname')}</label>
            <input value={sshHost} onChange={(e) => setSshHost(e.target.value)} placeholder="192.168.1.10" />
          </div>
          <div className="field">
            <label>{t('services.sshPort')}</label>
            <input type="number" value={sshPort} onChange={(e) => setSshPort(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.sshPassword')}</label>
            <input type="password" value={sshPass} onChange={(e) => setSshPass(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.sshPrivKey')}</label>
            <input value={sshKey} onChange={(e) => setSshKey(e.target.value)} placeholder={t('services.sshPrivKeyHint')} />
          </div>
          <div className="field" style={{ gridColumn: '1 / -1' }}>
            <label>{t('services.sshCommand')}</label>
            <input value={sshCmd} onChange={(e) => setSshCmd(e.target.value)} placeholder="whoami && uname -a" />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={runSsh} disabled={busy || !sshHost || !sshCmd}>
              {busy ? t('common.working') : t('services.sshRun')}
            </button>
          </div>
        </div>
        {sshResult && (
          <div className="exec-output">
            <div className="exec-output-header">
              <span>{t('exec.output')}</span>
            </div>
            <pre>{sshResult.StdOut || '(no stdout)'}{sshResult.StdErr ? `\n[stderr]\n${sshResult.StdErr}` : ''}</pre>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('services.extTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('services.extHint')}
        </p>
        <div className="pf-form">
          <div className="field">
            <label>{t('services.extName')}</label>
            <input value={extName} onChange={(e) => setExtName(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.extOs')}</label>
            <input value={extOs} onChange={(e) => setExtOs(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.extInit')}</label>
            <input value={extInit} onChange={(e) => setExtInit(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.extFile')}</label>
            <input type="file" onChange={(e) => setExtFile(e.target.files?.[0] || null)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={registerExtension} disabled={busy || !extName || !extFile}>
              {busy ? t('common.working') : t('services.extRegister')}
            </button>
          </div>
        </div>
        <table className="data">
          <thead>
            <tr>
              <th>{t('services.extLoaded')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {extNames.length === 0 && (
              <tr>
                <td colSpan={2} className="empty">
                  {t('services.extNone')}
                </td>
              </tr>
            )}
            {extNames.map((n) => (
              <tr key={n}>
                <td className="mono">{n}</td>
                <td>
                  <button type="button" className="btn sm" onClick={() => setCallName(n)}>
                    {t('services.extCall')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="pf-form" style={{ marginTop: 12 }}>
          <div className="field">
            <label>{t('services.extCallName')}</label>
            <input value={callName} onChange={(e) => setCallName(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('services.extExport')}</label>
            <input value={callExport} onChange={(e) => setCallExport(e.target.value)} placeholder="Run" />
          </div>
          <div className="field">
            <label>{t('services.extArgs')}</label>
            <input value={callArgs} onChange={(e) => setCallArgs(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-start' }}>
            <label style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
              <input type="checkbox" checked={callServerStore} onChange={(e) => setCallServerStore(e.target.checked)} />
              {t('services.extServerStore')}
            </label>
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={callExtension} disabled={busy || !callName || !callExport}>
              {busy ? t('common.working') : t('services.extCall')}
            </button>
          </div>
        </div>
        {callResult && (
          <div className="exec-output">
            <div className="exec-output-header">
              <span>{t('exec.output')}</span>
              {callResult.ServerStore && <span>{t('services.extServerStored')}</span>}
            </div>
            <pre>{callResult.Output || '(no output)'}</pre>
          </div>
        )}
      </div>

      {error && <div className="error-banner">{error}</div>}
    </>
  )
}
