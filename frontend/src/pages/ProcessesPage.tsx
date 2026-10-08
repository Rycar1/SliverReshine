import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import SessionPicker from '../components/SessionPicker'
import ProcessesTab from '../components/session/ProcessesTab'
import './pages.css'

export default function ProcessesPage() {
  const { t } = useTranslation()
  const [sessionId, setSessionId] = useState('')
  // The session's platform decides whether the tab offers the Windows-only
  // migrate and dump actions, so the picker's session is kept, not just its id.
  const [sessionOs, setSessionOs] = useState('')

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('host.processes')}</div>
          <div className="page-sub">{t('host.processesSub')}</div>
        </div>
      </div>
      <div className="card">
        <SessionPicker
          value={sessionId}
          onChange={(id, session) => {
            setSessionId(id)
            setSessionOs(session?.OS || '')
          }}
        />
      </div>
      {sessionId ? (
        <ProcessesTab key={sessionId} sessionId={sessionId} os={sessionOs} />
      ) : (
        <div className="empty">{t('host.pickSession')}</div>
      )}
    </div>
  )
}
