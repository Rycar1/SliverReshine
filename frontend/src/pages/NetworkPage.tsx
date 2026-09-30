import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import SessionPicker from '../components/SessionPicker'
import NetworkTab from '../components/session/NetworkTab'
import './pages.css'

export default function NetworkPage() {
  const { t } = useTranslation()
  const [sessionId, setSessionId] = useState('')

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('host.network')}</div>
          <div className="page-sub">{t('host.networkSub')}</div>
        </div>
      </div>
      <div className="card">
        <SessionPicker value={sessionId} onChange={setSessionId} />
      </div>
      {sessionId ? (
        <NetworkTab key={sessionId} sessionId={sessionId} />
      ) : (
        <div className="empty">{t('host.pickSession')}</div>
      )}
    </div>
  )
}
