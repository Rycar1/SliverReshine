import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { WindowsPrivilege } from '../../lib/types'
import CredentialPicker from '../common/CredentialPicker'

export default function TokensTab({ sessionId, os }: { sessionId: string; os: string }) {
  const { t } = useTranslation()
  const [action, setAction] = useState<'impersonate' | 'makeToken' | 'getSystem'>('impersonate')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [domain, setDomain] = useState('')
  const [hostingProcess, setHostingProcess] = useState('spoolsv.exe')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [running, setRunning] = useState(false)

  const [owner, setOwner] = useState('')
  const [privs, setPrivs] = useState<WindowsPrivilege[] | null>(null)
  const [busy, setBusy] = useState(false)

  const [runAsUser, setRunAsUser] = useState('')
  const [runAsProc, setRunAsProc] = useState('')
  const [runAsArgs, setRunAsArgs] = useState('')
  const [runAsOutput, setRunAsOutput] = useState('')

  const [exeTokenPath, setExeTokenPath] = useState('')
  const [exeTokenArgs, setExeTokenArgs] = useState('')
  const [exeTokenResult, setExeTokenResult] = useState('')

  const run = async () => {
    setRunning(true)
    setError('')
    setMessage('')
    try {
      if (action === 'impersonate') {
        await api.impersonate(sessionId, username)
        setMessage(t('tokens.impersonateOk', { username }))
      } else if (action === 'makeToken') {
        await api.makeToken(sessionId, username, password, domain)
        setMessage(t('tokens.makeTokenOk'))
      } else {
        await api.getSystem(sessionId, hostingProcess)
        setMessage(t('tokens.getSystemOk'))
      }
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }

  const revToSelf = async () => {
    setError('')
    setMessage('')
    try {
      await api.revToSelf(sessionId)
      setMessage(t('tokens.revToSelfOk'))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const loadOwner = async () => {
    setBusy(true)
    setError('')
    try {
      const d = await api.currentTokenOwner(sessionId)
      setOwner(d.owner)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const loadPrivs = async () => {
    setBusy(true)
    setError('')
    try {
      const d = await api.getPrivs(sessionId)
      setPrivs(d.privileges || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const runRunAs = async () => {
    setBusy(true)
    setError('')
    setRunAsOutput('')
    try {
      const d = await api.runAs(sessionId, runAsUser, runAsProc, runAsArgs)
      setRunAsOutput(d.async ? t('tokens.runAsAsync') : d.output)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const runExecuteToken = async () => {
    setBusy(true)
    setError('')
    setExeTokenResult('')
    try {
      const d = await api.executeToken(sessionId, exeTokenPath, exeTokenArgs ? exeTokenArgs.split(' ') : [], true)
      setExeTokenResult(
        `[${d.Status}] stdout:\n${d.Stdout}\nstderr:\n${d.Stderr}`,
      )
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (os !== 'windows') {
    return <div className="empty">{t('tokens.windowsOnly')}</div>
  }

  return (
    <div className="card">
      <div className="card-title">{t('tokens.title')}</div>
      <div className="form-grid" style={{ marginBottom: 12 }}>
        <div className="field">
          <label>{t('tokens.action')}</label>
          <select value={action} onChange={(e) => setAction(e.target.value as typeof action)}>
            <option value="impersonate">{t('tokens.modeImpersonate')}</option>
            <option value="makeToken">{t('tokens.modeMakeToken')}</option>
            <option value="getSystem">{t('tokens.modeGetSystem')}</option>
          </select>
        </div>
        {(action === 'impersonate' || action === 'makeToken') && (
          <div className="field">
            {/* The vault is the same store the harvest tab writes to, so a
                password recovered minutes ago is one click away instead of a
                copy-paste from another page. */}
            <CredentialPicker
              requirePlaintext={action === 'makeToken'}
              onChange={(c) => {
                if (!c) return
                setUsername(c.username)
                if (action === 'makeToken') setPassword(c.secret)
              }}
            />
          </div>
        )}
        {(action === 'impersonate' || action === 'makeToken') && (
          <div className="field">
            <label>{t('tokens.username')}</label>
            <input value={username} onChange={(e) => setUsername(e.target.value)} />
          </div>
        )}
        {action === 'makeToken' && (
          <>
            <div className="field">
              <label>{t('tokens.password')}</label>
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            <div className="field">
              <label>{t('tokens.domain')}</label>
              <input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="DOMAIN" />
            </div>
          </>
        )}
        {action === 'getSystem' && (
          <div className="field">
            <label>{t('tokens.hostingProcess')}</label>
            <input value={hostingProcess} onChange={(e) => setHostingProcess(e.target.value)} />
          </div>
        )}
        <div className="field" style={{ justifyContent: 'flex-end' }}>
          <button type="button" className="btn primary" onClick={run} disabled={running}>
            {running ? t('common.loading') : t('tokens.execute')}
          </button>
        </div>
      </div>
      <div className="toolbar" style={{ marginBottom: 12 }}>
        <button type="button" className="btn sm" onClick={revToSelf}>{t('tokens.revToSelf')}</button>
        <button type="button" className="btn sm" onClick={loadOwner} disabled={busy}>{t('tokens.tokenOwner')}</button>
        <button type="button" className="btn sm" onClick={loadPrivs} disabled={busy}>{t('tokens.getPrivs')}</button>
      </div>
      {owner && (
        <div className="toolbar" style={{ marginBottom: 12 }}>
          <span>{t('tokens.ownerLabel')}</span>
          <span className="mono">{owner}</span>
        </div>
      )}
      {error && <div className="error-banner">{error}</div>}
      {message && (
        <div className="success-banner">
          {message}
        </div>
      )}

      {privs && (
        <div className="card" style={{ marginTop: 14, padding: '12px 14px' }}>
          <div className="card-title" style={{ marginBottom: 10 }}>{t('tokens.privsTitle')}</div>
          <table className="data">
            <thead>
              <tr>
                <th>{t('tokens.thPriv')}</th>
                <th>{t('tokens.thEnabled')}</th>
                <th>{t('tokens.thDefault')}</th>
              </tr>
            </thead>
            <tbody>
              {privs.length === 0 && (
                <tr>
                  <td colSpan={3} className="empty">{t('tokens.noPrivs')}</td>
                </tr>
              )}
              {privs.map((p) => (
                <tr key={p.Name}>
                  <td className="mono">
                    {p.Name}
                    {p.UsedForAccess && <span className="mono"> · used</span>}
                  </td>
                  <td>{p.Enabled ? '✓' : '—'}</td>
                  <td>{p.EnabledByDefault ? '✓' : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="card" style={{ marginTop: 14, padding: '12px 14px' }}>
        <div className="card-title" style={{ marginBottom: 10 }}>{t('tokens.runAsTitle')}</div>
        {/* RunAs is the operation most likely to be fed from a dump: the account
            it needs is usually one mimikatz just recovered, and the password is
            not something the operator wants to retype. */}
        <div className="field" style={{ marginBottom: 8 }}>
          <CredentialPicker
            requirePlaintext
            onChange={(c) => {
              if (c) setRunAsUser(c.username)
            }}
          />
        </div>
        <div className="form-grid">
          <div className="field">
            <label>{t('tokens.username')}</label>
            <input value={runAsUser} onChange={(e) => setRunAsUser(e.target.value)} />
          </div>
          <div className="field">
            <label>{t('tokens.processName')}</label>
            <input value={runAsProc} onChange={(e) => setRunAsProc(e.target.value)} placeholder="C:\Windows\System32\cmd.exe" />
          </div>
          <div className="field">
            <label>{t('tokens.args')}</label>
            <input value={runAsArgs} onChange={(e) => setRunAsArgs(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={runRunAs} disabled={busy}>
              {t('tokens.runAs')}
            </button>
          </div>
        </div>
        {runAsOutput && <pre className="viewer-pre mono" style={{ marginTop: 8 }}>{runAsOutput}</pre>}
      </div>

      <div className="card" style={{ marginTop: 14, padding: '12px 14px' }}>
        <div className="card-title" style={{ marginBottom: 10 }}>{t('tokens.exeTokenTitle')}</div>
        <div className="form-grid">
          <div className="field" style={{ flex: 2 }}>
            <label>{t('tokens.path')}</label>
            <input value={exeTokenPath} onChange={(e) => setExeTokenPath(e.target.value)} placeholder="C:\Windows\System32\whoami.exe" />
          </div>
          <div className="field">
            <label>{t('tokens.args')}</label>
            <input value={exeTokenArgs} onChange={(e) => setExeTokenArgs(e.target.value)} />
          </div>
          <div className="field" style={{ justifyContent: 'flex-end' }}>
            <button type="button" className="btn primary" onClick={runExecuteToken} disabled={busy}>
              {t('tokens.exeToken')}
            </button>
          </div>
        </div>
        {exeTokenResult && <pre className="viewer-pre mono" style={{ marginTop: 8 }}>{exeTokenResult}</pre>}
      </div>
    </div>
  )
}
