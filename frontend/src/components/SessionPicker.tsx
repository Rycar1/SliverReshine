import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Session } from '../lib/types'

export default function SessionPicker({
  value,
  onChange,
  required,
}: {
  value: string
  onChange: (id: string, session?: Session) => void
  required?: string
}) {
  const { t } = useTranslation()
  const [sessions, setSessions] = useState<Session[]>([])
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const d = await api.sessions()
      setSessions(d.sessions || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    load()
    const timer = setInterval(load, 5000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    if (!value && sessions.length > 0) onChange(sessions[0].ID, sessions[0])
  }, [sessions, value, onChange])

  if (error) return <div className="error-banner">{error}</div>

  return (
    <div className="field">
      <label>{t('host.session')}</label>
      <select
        value={value}
        onChange={(e) => {
          const id = e.target.value
          onChange(id, sessions.find((s) => s.ID === id))
        }}
      >
        {required && <option value="">{required}</option>}
        {sessions.length === 0 && <option value="">{t('sessions.empty')}</option>}
        {sessions.map((s) => (
          <option key={s.ID} value={s.ID}>
            {s.Name} — {s.Hostname} ({s.OS}/{s.Arch})
          </option>
        ))}
      </select>
    </div>
  )
}
