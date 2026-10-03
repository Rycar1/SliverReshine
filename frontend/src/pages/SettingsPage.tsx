import { useEffect, useRef, useState, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useConnection } from '../lib/connection'
import type { AuthSettings } from '../lib/types'
import { useToast } from '../components/common/Toast'
import { LANGS, currentLang, setLang, type Lang } from '../lib/lang'
import './pages.css'

interface LoadedConfig {
  operator: string
  lhost: string
  lport: number
  hasCerts: boolean
}

// The loaded operator config holds the client certificate AND the mTLS private
// key. Both the raw file text and the parsed summary are held in React state
// for the lifetime of this page and nowhere else -- no sessionStorage, no
// localStorage, no IndexedDB, no cookie.
//
// It used to live in sessionStorage, on the theory that the tab scope limited
// the exposure. That narrowed the window without changing the property that
// matters: Web Storage is readable by any script running on this origin, so the
// key is only ever as trustworthy as the least trustworthy script the console
// loads, and it stayed readable for as long as the tab lived. A key that cannot
// be read out of a storage API is a key an injected script has to steal from a
// live closure instead, which is a materially harder target.
//
// The accepted cost is that a page reload forgets the file and the operator
// selects it again. That is the whole point: an unlocked private key should not
// outlive the operator's attention. Re-selecting the profile IS the recovery
// path, and there is deliberately no silent one.
const PROFILE_KEY = 'sliverreshine.activeProfile'

function parseConfig(text: string): LoadedConfig {
  const data = JSON.parse(text)
  const lport = Number(data.lport) || 0
  const hasCerts = Boolean(data.ca_certificate && data.certificate && data.private_key)
  return {
    operator: data.operator || '',
    lhost: data.lhost || '',
    lport,
    hasCerts,
  }
}

export default function SettingsPage() {
  const { connected, version, refresh } = useConnection()
  const [profiles, setProfiles] = useState<string[]>([])
  const [activeProfile, setActiveProfile] = useState('')
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [loadingProfiles, setLoadingProfiles] = useState(false)
  const { t } = useTranslation()
  // 语言在 setLang 内部持久化；useTranslation 会在语言变化时重渲染本页。
  const [language, setLanguage] = useState<Lang>(currentLang())
  const chooseLang = (next: Lang) => {
    setLang(next)
    setLanguage(next)
  }

  const [configContent, setConfigContent] = useState('')
  const [configInfo, setConfigInfo] = useState<LoadedConfig | null>(null)
  const [connecting, setConnecting] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)

  const loadProfiles = async () => {
    try {
      const data = await api.listProfiles()
      setProfiles(data.profiles || [])
    } catch (e) {
      setError(`${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  useEffect(() => {
    loadProfiles()
    // Nothing about the operator config is restored here, by design. Anything
    // this effect could put back would have had to survive in Web Storage
    // first, and the private key must not. The console therefore comes up with
    // no file loaded and the operator picks one again -- see the connect hint.

    const profile = localStorage.getItem(PROFILE_KEY)
    if (profile) setActiveProfile(profile)
  }, [])

  const useProfile = async (name: string) => {
    setLoadingProfiles(true)
    setError('')
    setSuccess('')
    try {
      await api.useProfile(name)
      setSuccess(t('settings.usingProfile', { name }))
      setActiveProfile(name)
      localStorage.setItem(PROFILE_KEY, name)
      refresh()
    } catch (e) {
      setError(`${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setLoadingProfiles(false)
    }
  }

  const handleFile = (e: ChangeEvent<HTMLInputElement>) => {
    setError('')
    setSuccess('')
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      const text = String(reader.result || '')
      try {
        const info = parseConfig(text)
        setConfigContent(text)
        setConfigInfo(info)
      } catch (err) {
        setConfigContent('')
        setConfigInfo(null)
        setError(`${t('settings.invalidConfig')}: ${(err as Error).message}`)
      }
    }
    reader.onerror = () => {
      setConfigContent('')
      setConfigInfo(null)
      setError(t('settings.readError'))
    }
    reader.readAsText(file)
  }

  const connectFromFile = async () => {
    if (!configContent) {
      setError(t('settings.noConfigLoaded'))
      return
    }
    setConnecting(true)
    setError('')
    setSuccess('')
    try {
      await api.connect({ content: configContent })
      setSuccess(t('settings.connectedMsg'))
      refresh()
      loadProfiles()
    } catch (e) {
      setError(`${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setConnecting(false)
    }
  }

  const disconnect = async () => {
    setError('')
    setSuccess('')
    try {
      await api.disconnect()
      // Drop the operator identity as well as the session. The key only ever
      // existed in this component's state, so clearing it below is what
      // forgets it; there is no storage entry left behind to clean up.
      setConfigContent('')
      setConfigInfo(null)
      setSuccess(t('settings.disconnectedMsg'))
      refresh()
    } catch (e) {
      setError(`${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  const toast = useToast()

  // --- Console authentication ---------------------------------------------
  // The console credential is the same pair the browser login prompt checks,
  // which is why the panel lives here rather than on a page of its own.
  const [auth, setAuth] = useState<AuthSettings | null>(null)
  const [authUser, setAuthUser] = useState('')
  const [authPass, setAuthPass] = useState('')
  const [authConfirm, setAuthConfirm] = useState('')
  const [authCurrent, setAuthCurrent] = useState('')
  const [authError, setAuthError] = useState('')
  const [authBusy, setAuthBusy] = useState(false)

  const loadAuth = async () => {
    try {
      const a = await api.authGet()
      setAuth(a)
      setAuthUser(a?.username || '')
    } catch (e) {
      setAuth(null)
      setAuthError(`${t('common.failed')}: ${(e as Error).message}`)
    }
  }

  useEffect(() => {
    loadAuth()
  }, [])

  const saveAuth = async () => {
    setAuthError('')
    // Validate locally: a mismatch or a short password is an operator typo, not
    // a conversation the server needs to have.
    if (authPass.length < 8) {
      setAuthError(t('auth.tooShort'))
      return
    }
    if (authPass !== authConfirm) {
      setAuthError(t('auth.mismatch'))
      return
    }
    setAuthBusy(true)
    try {
      const res = await api.authPut(authUser.trim(), authPass, authCurrent)
      if (res.ok === false) {
        setAuthError(res.message || t('common.failed'))
      } else {
        toast.push('success', t('auth.saved'))
        setAuthPass('')
        setAuthConfirm('')
        setAuthCurrent('')
        loadAuth()
      }
    } catch (e) {
      setAuthError(`${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setAuthBusy(false)
    }
  }

  const authState = auth ? (auth.enabled ? t('auth.enabled') : t('auth.disabled')) : t('auth.unknown')

  const authPanel = (
    <div className="card">
      <div className="card-title">{t('auth.title')}</div>
      <p className="page-sub" style={{ marginBottom: 12 }}>
        {t('auth.sub')}
      </p>
      <div className="form-grid">
        <div className="field">
          <label htmlFor="auth-current-user">{t('auth.current')}</label>
          <input id="auth-current-user" value={auth?.username || ''} readOnly />
        </div>
        <div className="field">
          <label htmlFor="auth-state">{t('auth.stateLabel')}</label>
          <input id="auth-state" value={authState} readOnly />
        </div>
        <div className="field">
          <label htmlFor="auth-source">{t('auth.source')}</label>
          <input id="auth-source" value={auth?.source || '-'} readOnly />
        </div>
        <div className="field">
          <label htmlFor="auth-new-user">{t('auth.username')}</label>
          <input id="auth-new-user" value={authUser} onChange={(e) => setAuthUser(e.target.value)} autoComplete="username" />
        </div>
        <div className="field">
          <label htmlFor="auth-new-pass">{t('auth.password')}</label>
          <input id="auth-new-pass" type="password" value={authPass} onChange={(e) => setAuthPass(e.target.value)} autoComplete="new-password" />
        </div>
        <div className="field">
          <label htmlFor="auth-confirm">{t('auth.confirm')}</label>
          <input id="auth-confirm" type="password" value={authConfirm} onChange={(e) => setAuthConfirm(e.target.value)} autoComplete="new-password" />
        </div>
        <div className="field">
          <label htmlFor="auth-current-pass">{t('auth.currentPassword')}</label>
          <input id="auth-current-pass" type="password" value={authCurrent} onChange={(e) => setAuthCurrent(e.target.value)} autoComplete="current-password" />
        </div>
      </div>
      <div className="toolbar" style={{ marginTop: 12 }}>
        <button type="button" className="btn primary" onClick={saveAuth} disabled={authBusy}>
          {authBusy ? t('common.working') : t('common.save')}
        </button>
      </div>
      {authError && <div className="error-banner">{authError}</div>}
      <p className="page-sub" style={{ marginTop: 12, marginBottom: 0 }}>
        {t('auth.note')}
      </p>
    </div>
  )


  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('settings.title')}</div>
          <div className="page-sub">{t('settings.sub')}</div>
        </div>
      </div>
      <div className="card">
        <div className="card-title">{t('settings.languageTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('settings.languageSub')}
        </p>
        <div className="toolbar" style={{ flexWrap: 'wrap' }}>
          {LANGS.map((l) => (
            <button
              type="button"
              key={l.value}
              className={`btn ${language === l.value ? 'primary' : ''}`}
              onClick={() => chooseLang(l.value)}
              aria-pressed={language === l.value}
            >
              {l.native}
            </button>
          ))}
        </div>
      </div>

      {authPanel}

      <div className="card">
        <div className="card-title">{t('settings.connTitle')}</div>
        <p style={{ marginBottom: 8 }}>
          {t('settings.status')}:{' '}
          <span className={`badge ${connected ? 'green' : 'red'}`}>
            {connected ? t('app.connected', { version }) : t('app.notConnected')}
          </span>
        </p>
        <div className="toolbar">
          {connected ? (
            <button type="button" className="btn danger" onClick={disconnect}>
              {t('settings.disconnect')}
            </button>
          ) : (
            <span className="empty" style={{ padding: 0 }}>
              {t('settings.connectHint')}
            </span>
          )}
        </div>
      </div>

      <div className="card">
        <div className="card-title">{t('settings.profilesTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('settings.profilesSub')}
        </p>
        <div className="toolbar" style={{ flexWrap: 'wrap' }}>
          {profiles.map((p) => (
            <button type="button"
              key={p}
              className={`btn ${activeProfile === p ? 'primary' : ''}`}
              onClick={() => useProfile(p)}
              disabled={loadingProfiles}
            >
              {p}
            </button>
          ))}
          {profiles.length === 0 && (
            <span className="empty" style={{ padding: 0 }}>
              {t('settings.noProfiles')}
            </span>
          )}
        </div>
      </div>

      <div className="card">
        <div className="card-title">{t('settings.loadConfigTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>
          {t('settings.loadConfigSub')}
        </p>
        <div className="toolbar">
          <label className="btn">
            {t('settings.selectFile')}
            <input
              ref={fileRef}
              type="file"
              accept=".json,.cfg,application/json"
              style={{ display: 'none' }}
              onChange={handleFile}
            />
          </label>
        </div>

        {configInfo && (
          <div className="form-grid" style={{ marginTop: 16 }}>
            <div className="field">
              <label>{t('settings.configOperator')}</label>
              <input value={configInfo.operator} readOnly />
            </div>
            <div className="field">
              <label>{t('settings.configLhost')}</label>
              <input value={configInfo.lhost} readOnly />
            </div>
            <div className="field">
              <label>{t('settings.configLport')}</label>
              <input value={configInfo.lport} readOnly />
            </div>
            <div className="field">
              <label>{t('settings.configCerts')}</label>
              <input
                value={configInfo.hasCerts ? t('settings.configHasCerts') : t('settings.configNoCerts')}
                readOnly
              />
            </div>
          </div>
        )}

        {/* The key lives in memory only, so a reload really does lose it. Say so
            here rather than letting the operator discover it as a dead Connect
            button after a refresh. */}
        <p className="page-sub" style={{ marginTop: 12, marginBottom: 0 }}>
          {t('settings.configMemoryOnly')}
        </p>

        <div className="toolbar" style={{ marginTop: 16 }}>
          <button
            type="button"
            className="btn primary"
            onClick={connectFromFile}
            disabled={!configContent || connecting}
          >
            {t('settings.connect')}
          </button>
        </div>
      </div>

      {error && <div className="error-banner">{error}</div>}
      {success && (
        <div
          className="error-banner"
          style={{
            borderColor: 'var(--green)',
            color: 'var(--green)',
            background: 'var(--success-bg)',
          }}
        >
          {success}
        </div>
      )}
    </div>
  )
}
