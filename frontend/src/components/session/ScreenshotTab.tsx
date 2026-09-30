import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'

interface Shot {
  id: number
  data: string
  time: string
}

export default function ScreenshotTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const [taking, setTaking] = useState(false)
  const [error, setError] = useState('')
  const [shots, setShots] = useState<Shot[]>([])
  const [selected, setSelected] = useState<number | null>(null)

  const take = async () => {
    setTaking(true)
    setError('')
    try {
      const r = await api.screenshot(sessionId)
      const now = new Date().toLocaleTimeString()
      const id = Date.now()
      setShots((prev) => [{ id, data: `data:image/png;base64,${r.Data}`, time: now }, ...prev])
      setSelected(id)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setTaking(false)
    }
  }

  const current = shots.find((s) => s.id === selected)

  return (
    <div className="card">
      <div className="screenshot-actions">
        <button type="button" className="btn primary" onClick={take} disabled={taking}>
          {taking ? t('screenshot.taking') : t('screenshot.take')}
        </button>
        {shots.length > 0 && (
          <button type="button" className="btn sm danger" onClick={() => { setShots([]); setSelected(null) }}>
            {t('screenshot.clear')}
          </button>
        )}
      </div>
      {error && <div className="error-banner">{error}</div>}

      {shots.length === 0 && !error && <div className="empty">{t('screenshot.empty')}</div>}

      {current && <img className="screenshot-img" src={current.data} alt="screenshot" />}

      {shots.length > 1 && (
        <div style={{ display: 'flex', gap: 6, marginTop: 10, flexWrap: 'wrap' }}>
          {shots.map((s) => (
            <button type="button"
              key={s.id}
              onClick={() => setSelected(s.id)}
              style={{
                width: 80,
                height: 50,
                borderRadius: 4,
                overflow: 'hidden',
                border: s.id === selected ? '2px solid var(--accent)' : '1px solid var(--border)',
                padding: 0,
                cursor: 'pointer',
                background: 'var(--bg)',
                flexShrink: 0,
              }}
              title={s.time}
            >
              <img src={s.data} alt={s.time} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
