import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { AISettings } from '../lib/types'
import { useToast } from '../components/common/Toast'
import './pages.css'

/**
 * AIConfigPage is the one place an operator sets up the model that the
 * console's AI features use: endpoint, key and model.
 *
 * The configuration is stored server-side, so every other AI feature --
 * collection, summaries, reports -- reads the same values with no
 * configuration of its own. The API never echoes the key back, so the key
 * field starts blank and leaving it blank on save keeps the stored key.
 */
export default function AIConfigPage() {
  const { t } = useTranslation()
  const toast = useToast()

  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [loadingModels, setLoadingModels] = useState(false)
  const [settings, setSettings] = useState<AISettings | null>(null)
  const [error, setError] = useState('')

  const [baseURL, setBaseURL] = useState('')
  const [model, setModel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState(60)
  const [thinking, setThinking] = useState(false)
  const [models, setModels] = useState<string[]>([])

  const apply = useCallback((s: AISettings) => {
    setSettings(s)
    setBaseURL(s.baseURL || '')
    setModel(s.model || '')
    setTimeoutSeconds(s.timeoutSeconds > 0 ? s.timeoutSeconds : 60)
    setThinking(s.thinking)
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      apply(await api.aiSettings())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [apply])

  useEffect(() => {
    load()
  }, [load])

  // Fetch the endpoint's model list. It sends the values currently in the
  // form, not the saved ones, so a model can be chosen before anything is
  // written. A blank field tells the server to use the stored value.
  const refreshModels = useCallback(async () => {
    setLoadingModels(true)
    setError('')
    try {
      const res = await api.aiModels({
        baseURL: baseURL.trim() || undefined,
        apiKey: apiKey.trim() || undefined,
      })
      const list = res.models || []
      setModels(list)
      if (list.length === 0) toast.push('info', t('aiConfig.noModels'))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoadingModels(false)
    }
  }, [baseURL, apiKey, t, toast])

  const save = useCallback(async () => {
    setSaving(true)
    setError('')
    try {
      const saved = await api.aiSettingsUpdate({
        baseURL: baseURL.trim(),
        model: model.trim(),
        apiKey: apiKey.trim() || undefined,
        timeoutSeconds,
        thinking,
      })
      apply(saved)
      setApiKey('')
      toast.push('success', t('aiConfig.saved'))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }, [baseURL, model, apiKey, timeoutSeconds, thinking, apply, t, toast])

  const configured = Boolean(settings?.hasKey && baseURL.trim() && model.trim())

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('aiConfig.title')}</div>
          <div className="page-sub">{t('aiConfig.sub')}</div>
        </div>
      </div>

      <div className="card">
        <div className="card-title">{t('aiConfig.statusTitle')}</div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <span className={'badge ' + (configured ? 'green' : 'yellow')}>
            {configured ? t('aiConfig.ready') : t('aiConfig.notReady')}
          </span>
          <span className={'badge ' + (settings?.hasKey ? 'green' : 'red')}>
            {t('aiConfig.key')}: {settings?.hasKey ? t('aiConfig.keySet') : t('aiConfig.keyMissing')}
          </span>
          {settings?.model && <span className="badge gray">{t('aiConfig.model')}: {settings.model}</span>}
        </div>
        {settings && !settings.managed && (
          <div className="alert" style={{ marginTop: 10 }}>{t('aiConfig.unmanaged')}</div>
        )}
      </div>

      <div className="card">
        <div className="card-title">{t('aiConfig.formTitle')}</div>
        <p className="page-sub" style={{ marginBottom: 12 }}>{t('aiConfig.formSub')}</p>

        <div className="form-grid">
          <div className="field">
            <label htmlFor="ai-base-url">{t('aiConfig.baseURL')}</label>
            <input
              id="ai-base-url"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder="https://api.openai.com/v1"
              spellCheck={false}
              autoComplete="off"
            />
            <p className="page-sub" style={{ margin: '4px 0 0' }}>{t('aiConfig.baseURLHint')}</p>
          </div>

          <div className="field">
            <label htmlFor="ai-api-key">{t('aiConfig.apiKey')}</label>
            <input
              id="ai-api-key"
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={settings?.hasKey ? t('aiConfig.keyKeep') : t('aiConfig.keyPlaceholder')}
              autoComplete="new-password"
            />
            <p className="page-sub" style={{ margin: '4px 0 0' }}>{t('aiConfig.apiKeyHint')}</p>
          </div>

          <div className="field">
            <label htmlFor="ai-model">{t('aiConfig.model')}</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                id="ai-model"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder="gpt-4o-mini"
                spellCheck={false}
                autoComplete="off"
                list="ai-model-options"
              />
              <datalist id="ai-model-options">
                {models.map((m) => (
                  <option key={m} value={m} />
                ))}
              </datalist>
              <button
                type="button"
                className="btn"
                onClick={refreshModels}
                disabled={loadingModels}
                title={t('aiConfig.refreshModelsHint')}
              >
                {loadingModels ? t('aiConfig.loading') : t('aiConfig.refreshModels')}
              </button>
            </div>
            {models.length > 0 && (
              <p className="page-sub" style={{ margin: '4px 0 0' }}>
                {t('aiConfig.modelCount', { count: models.length })}
              </p>
            )}
          </div>

          <div className="field">
            <label htmlFor="ai-timeout">{t('aiConfig.timeout')}</label>
            <input
              id="ai-timeout"
              type="number"
              min={5}
              value={timeoutSeconds}
              onChange={(e) => setTimeoutSeconds(Number(e.target.value) || 0)}
            />
            <p className="page-sub" style={{ margin: '4px 0 0' }}>{t('aiConfig.timeoutHint')}</p>
          </div>
        </div>

        <div className="field" style={{ marginTop: 12 }}>
          <label htmlFor="ai-thinking" style={{ display: 'flex', gap: 8, alignItems: 'center', cursor: 'pointer' }}>
            <input
              id="ai-thinking"
              type="checkbox"
              checked={thinking}
              onChange={(e) => setThinking(e.target.checked)}
            />
            {t('aiConfig.thinking')}
          </label>
          <p className="page-sub" style={{ margin: '4px 0 0' }}>{t('aiConfig.thinkingHint')}</p>
        </div>

        <div className="toolbar" style={{ marginTop: 14 }}>
          <button type="button" className="btn primary" onClick={save} disabled={saving || loading}>
            {saving ? t('aiConfig.saving') : t('aiConfig.save')}
          </button>
          <button type="button" className="btn" onClick={load} disabled={saving || loading}>
            {t('aiConfig.reload')}
          </button>
        </div>

        {error && <div className="error-banner" style={{ marginTop: 12 }}>{error}</div>}
      </div>
    </div>
  )
}
