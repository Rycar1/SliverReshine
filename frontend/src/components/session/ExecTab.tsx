import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { ExecResult } from '../../lib/types'

export default function ExecTab({ sessionId }: { sessionId: string }) {
  const { t } = useTranslation()
  const [path, setPath] = useState('')
  const [args, setArgs] = useState('')
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<ExecResult | null>(null)

  const run = async () => {
    if (!path) return
    setRunning(true)
    setError('')
    setResult(null)
    try {
      const r = await api.exec(
        sessionId,
        path,
        args
          .split(/\s+/)
          .map((s) => s.trim())
          .filter(Boolean),
      )
      setResult(r)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="card">
      <div className="card-title">{t('exec.title')}</div>
      <p className="page-sub" style={{ marginBottom: 12 }}>
        {t('exec.hint')}
      </p>
      <div className="exec-form">
        <input
          className="exec-path"
          type="text"
          placeholder={t('exec.path')}
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && run()}
        />
        <input
          className="exec-args"
          type="text"
          placeholder={t('exec.args')}
          value={args}
          onChange={(e) => setArgs(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && run()}
        />
        <button type="button" className="btn primary" onClick={run} disabled={running || !path}>
          {running ? t('exec.running') : t('exec.run')}
        </button>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {result && (
        <div className="exec-output">
          <div className="exec-output-header">
            <span>
              {t('exec.exitCode')}: {result.Status}
            </span>
            <span className="mono">PID {result.PID}</span>
          </div>
          {(result.Stdout || result.Stderr) && (
            <pre>
              {result.Stdout && `[${t('exec.stdout')}]\n${result.Stdout}\n`}
              {result.Stderr && `[${t('exec.stderr')}]\n${result.Stderr}`}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}
