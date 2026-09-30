import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { RpcMethod } from '../lib/types'
import { useToast } from '../components/common/Toast'
import './pages.css'
import './rawrpc.css'

/**
 * Raw RPC console.
 *
 * The modelled pages cover the everyday Sliver workflows. This page covers
 * everything else: it enumerates the generated SliverRPC client interface and
 * lets you call any method directly with a protojson body. That is what makes
 * the "full Sliver feature set" claim true rather than aspirational — methods
 * without a dedicated page (armory internals, crackstation operations, and
 * anything upstream adds later) are still reachable.
 */
export default function RawRpcPage() {
  const { t } = useTranslation()
  const toast = useToast()

  const [methods, setMethods] = useState<RpcMethod[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [group, setGroup] = useState('all')

  const [selected, setSelected] = useState<RpcMethod | null>(null)
  const [body, setBody] = useState('{}')
  const [timeout, setTimeoutSec] = useState(60)
  const [running, setRunning] = useState(false)
  const [response, setResponse] = useState<string>('')
  const [responseError, setResponseError] = useState('')

  const load = async () => {
    setLoading(true)
    try {
      const data = await api.rpcMethods()
      setMethods(data.methods || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  const groups = useMemo(() => {
    const seen = new Map<string, number>()
    for (const m of methods) seen.set(m.group, (seen.get(m.group) || 0) + 1)
    return Array.from(seen.entries()).sort((a, b) => a[0].localeCompare(b[0]))
  }, [methods])

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return methods.filter((m) => {
      if (group !== 'all' && m.group !== group) return false
      if (!q) return true
      return (
        m.name.toLowerCase().includes(q) ||
        m.inputType.toLowerCase().includes(q) ||
        m.outputType.toLowerCase().includes(q)
      )
    })
  }, [methods, filter, group])

  const pick = (m: RpcMethod) => {
    setSelected(m)
    setResponse('')
    setResponseError('')
    // Seed an empty object: most Get*/List* calls take no arguments, and an
    // empty body is accepted for every request message.
    setBody('{}')
  }

  const run = async () => {
    if (!selected) return
    let parsed: unknown
    try {
      parsed = body.trim() === '' ? {} : JSON.parse(body)
    } catch (e) {
      setResponseError(`${t('rawrpc.invalidJson')}: ${(e as Error).message}`)
      return
    }

    setRunning(true)
    setResponse('')
    setResponseError('')
    try {
      const res = await api.rpcCall(selected.name, parsed, timeout)
      setResponse(JSON.stringify(res, null, 2))
      if (selected.streaming) {
        toast.push('success', t('rawrpc.streamDone', { count: res.count ?? 0 }))
      } else {
        toast.push('success', t('rawrpc.ok', { name: selected.name }))
      }
    } catch (e) {
      setResponseError((e as Error).message)
    } finally {
      setRunning(false)
    }
  }

  const copyResponse = async () => {
    try {
      await navigator.clipboard.writeText(response)
      toast.push('success', t('rawrpc.copied'))
    } catch {
      toast.push('error', t('rawrpc.copyFailed'))
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('rawrpc.title')}</div>
          <div className="page-sub">{t('rawrpc.sub', { count: methods.length })}</div>
        </div>
        <div className="toolbar">
          <button type="button" className="btn" onClick={load} disabled={loading}>
            {t('common.refresh')}
          </button>
        </div>
      </div>

      {error && <div className="error-banner">{error}</div>}

      <div className="rawrpc-layout">
        <aside className="rawrpc-list card">
          <div className="rawrpc-list-controls">
            <input
              type="search"
              value={filter}
              placeholder={t('rawrpc.filterPlaceholder')}
              onChange={(e) => setFilter(e.target.value)}
            />
            <select value={group} onChange={(e) => setGroup(e.target.value)}>
              <option value="all">{t('rawrpc.allGroups')}</option>
              {groups.map(([g, n]) => (
                <option key={g} value={g}>
                  {g} ({n})
                </option>
              ))}
            </select>
          </div>

          <div className="rawrpc-list-body">
            {loading && <div className="rawrpc-hint">{t('common.loading')}</div>}
            {!loading && visible.length === 0 && (
              <div className="rawrpc-hint">{t('rawrpc.noMatch')}</div>
            )}
            {visible.map((m) => (
              <button
                type="button"
                key={m.name}
                className={`rawrpc-item ${selected?.name === m.name ? 'active' : ''}`}
                onClick={() => pick(m)}
                title={`${m.name}(${m.inputType}) -> ${m.outputType}`}
              >
                <span className="rawrpc-item-name mono">{m.name}</span>
                {m.streaming && <span className="rawrpc-tag">{t('rawrpc.stream')}</span>}
              </button>
            ))}
          </div>
        </aside>

        <section className="rawrpc-detail card">
          {!selected ? (
            <div className="rawrpc-hint">{t('rawrpc.pickMethod')}</div>
          ) : (
            <>
              <div className="rawrpc-detail-head">
                <div>
                  <div className="rawrpc-detail-name mono">{selected.name}</div>
                  <div className="rawrpc-detail-types mono">
                    {selected.inputType} → {selected.outputType}
                  </div>
                </div>
                <div className="rawrpc-detail-actions">
                  <label className="rawrpc-timeout">
                    <span>{t('rawrpc.timeout')}</span>
                    <input
                      type="number"
                      min={1}
                      max={3600}
                      value={timeout}
                      onChange={(e) => setTimeoutSec(Number(e.target.value) || 60)}
                    />
                  </label>
                  <button type="button" className="btn primary" onClick={run} disabled={running}>
                    {running ? t('rawrpc.running') : t('rawrpc.invoke')}
                  </button>
                </div>
              </div>

              <div className="rawrpc-editors">
                <div className="rawrpc-pane">
                  <div className="rawrpc-pane-title">{t('rawrpc.request')}</div>
                  <textarea
                    className="rawrpc-code mono"
                    spellCheck={false}
                    value={body}
                    onChange={(e) => setBody(e.target.value)}
                  />
                </div>
                <div className="rawrpc-pane">
                  <div className="rawrpc-pane-title">
                    {t('rawrpc.response')}
                    {response && (
                      <button type="button" className="btn sm" onClick={copyResponse}>
                        {t('rawrpc.copy')}
                      </button>
                    )}
                  </div>
                  {responseError ? (
                    <pre className="rawrpc-code rawrpc-error mono">{responseError}</pre>
                  ) : (
                    <pre className="rawrpc-code mono">{response || t('rawrpc.emptyResponse')}</pre>
                  )}
                </div>
              </div>
            </>
          )}
        </section>
      </div>
    </div>
  )
}
