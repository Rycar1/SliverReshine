import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { EnvVar } from '../../lib/types'

export default function EnvTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const [env, setEnv] = useState<EnvVar[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const d = await api.env(sessionId)
      setEnv(d.env || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    load()
  }, [load])

  const set = async () => {
    if (!key) return
    try {
      await api.setEnv(sessionId, key, value)
      setKey('')
      setValue('')
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const unset = async (k: string) => {
    try {
      await api.unsetEnv(sessionId, k)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="card card-flush">
      <div style={{ padding: '14px 16px' }}>
        <div className="env-form">
          <input
            type="text"
            placeholder={t('env.key')}
            value={key}
            onChange={(e) => setKey(e.target.value)}
          />
          <input
            type="text"
            placeholder={t('env.value')}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            style={{ flex: 1, minWidth: 200 }}
          />
          <button type="button" className="btn sm primary" onClick={set} disabled={!key}>
            {t('env.set')}
          </button>
          <button type="button" className="btn sm" onClick={load}>
            {t('env.refresh')}
          </button>
        </div>
        {error && <div className="error-banner">{error}</div>}
      </div>
      {loading ? (
        <div className="empty">{t('common.loading')}</div>
      ) : env.length === 0 ? (
        <div className="empty">{t('env.empty')}</div>
      ) : (
        <table className="data">
          <thead>
            <tr>
              <th>{t('env.key')}</th>
              <th>{t('env.value')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {env.map((v) => (
              <tr key={v.Key}>
                <td className="mono">{v.Key}</td>
                <td className="mono">{v.Value}</td>
                <td>
                  <button type="button" className="btn sm danger" onClick={() => unset(v.Key)}>
                    {t('env.unset')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
