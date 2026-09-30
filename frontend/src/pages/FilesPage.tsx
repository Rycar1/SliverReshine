import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import SessionPicker from '../components/SessionPicker'
import FilesTab from '../components/session/FilesTab'
import './pages.css'

export default function FilesPage() {
  const { t } = useTranslation()
  const [sessionId, setSessionId] = useState('')
  const [os, setOs] = useState<string | undefined>(undefined)

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('host.files')}</div>
          <div className="page-sub">{t('host.filesSub')}</div>
        </div>
      </div>
      <div className="card">
        <SessionPicker
          value={sessionId}
          onChange={(id, session) => {
            setSessionId(id)
            setOs(session?.OS)
          }}
        />
      </div>
      {sessionId ? (
        <FilesTab key={sessionId} sessionId={sessionId} os={os} />
      ) : (
        <div className="empty">{t('host.pickSession')}</div>
      )}
    </div>
  )
}
