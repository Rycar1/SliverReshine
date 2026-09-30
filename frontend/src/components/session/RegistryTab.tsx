import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import ConfirmDialog from '../common/ConfirmDialog'

const HIVES = ['HKEY_LOCAL_MACHINE', 'HKEY_CURRENT_USER', 'HKEY_CLASSES_ROOT', 'HKEY_USERS', 'HKEY_CURRENT_CONFIG']

interface RegItem {
  name: string
  isKey: boolean
}

export default function RegistryTab({ sessionId, os }: { sessionId: string; os: string }) {
  const { t } = useTranslation()
  const [hive, setHive] = useState(HIVES[0])
  const [path, setPath] = useState('')
  const [items, setItems] = useState<RegItem[]>([])
  const [readValue, setReadValue] = useState<{ path: string; key: string; value: string } | null>(null)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const [writeKey, setWriteKey] = useState('')
  const [writeValue, setWriteValue] = useState('')
  const [writeType, setWriteType] = useState('string')
  const [confirmDel, setConfirmDel] = useState<RegItem | null>(null)
  const [deleting, setDeleting] = useState(false)

  const browse = async (p: string) => {
    if (os !== 'windows') return
    setLoading(true)
    setError('')
    setReadValue(null)
    try {
      const [keys, values] = await Promise.all([
        api.regSubKeys(sessionId, hive, p),
        api.regValues(sessionId, hive, p),
      ])
      const all: RegItem[] = [
        ...(keys.keys || []).map((k) => ({ name: k, isKey: true })),
        ...(values.values || []).map((v) => ({ name: v, isKey: false })),
      ]
      setItems(all)
      setPath(p)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const click = (item: RegItem) => {
    if (item.isKey) {
      browse(joinPath(path, item.name))
    } else {
      read(item.name)
    }
  }

  const read = async (key: string) => {
    setError('')
    try {
      const r = await api.regRead(sessionId, hive, path, key)
      setReadValue({ path, key, value: r.Value })
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const write = async (key: string, value: string) => {
    setError('')
    setMessage('')
    try {
      await api.regWrite(sessionId, hive, path, key, value, writeType)
      setMessage(t('registry.wrote', { key }))
      browse(path)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const del = async (item: RegItem) => {
    setDeleting(true)
    setError('')
    setMessage('')
    try {
      await api.regDeleteKey(sessionId, hive, path, item.name)
      setMessage(t('registry.deleted', { key: item.name }))
      if (readValue && readValue.key === item.name) setReadValue(null)
      browse(path)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setDeleting(false)
      setConfirmDel(null)
    }
  }

  const joinPath = (base: string, name: string) => (base ? `${base}\\${name}` : name)

  const switchHive = (h: string) => {
    setHive(h)
    setPath('')
    setItems([])
    setReadValue(null)
  }

  if (os !== 'windows') {
    return <div className="empty">{t('registry.hint')}</div>
  }

  return (
    <div className="card">
      <div className="card-title">{t('registry.title')}</div>
      <div className="reg-form">
        <div className="field">
          <label>{t('registry.hive')}</label>
          <select value={hive} onChange={(e) => switchHive(e.target.value)}>
            {HIVES.map((h) => (
              <option key={h} value={h}>
                {h}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>{t('registry.path')}</label>
          <input value={path} onChange={(e) => setPath(e.target.value)} />
        </div>
        <div className="field" style={{ justifyContent: 'flex-end' }}>
          <button type="button" className="btn primary" onClick={() => browse(path)}>
            {t('registry.browse')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      {message && (
        <div
          className="error-banner"
          style={{
            borderColor: 'var(--green)',
            color: 'var(--green)',
            background: 'var(--success-bg)',
          }}
        >
          {message}
        </div>
      )}

      {loading ? (
        <div className="empty">{t('common.loading')}</div>
      ) : (
        <div className="reg-cols">
          <div className="card card-flush">
            <div style={{ padding: '10px 14px', borderBottom: '1px solid var(--border)', fontSize: 'var(--fs-sm)', color: 'var(--text-dim)' }}>
              {t('registry.subkeys')} / {t('registry.values')}
            </div>
            {items.length === 0 ? (
              <div className="empty">{t('registry.empty')}</div>
            ) : (
              <div style={{ padding: 6 }}>
                {items.map((it) => (
                  <div className="reg-item" key={`${it.isKey}-${it.name}`} onClick={() => click(it)}>
                    {it.isKey ? (
                      <svg className="reg-icon dir" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                      </svg>
                    ) : (
                      <svg className="reg-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                        <path d="M14 2v6h6" />
                      </svg>
                    )}
                    <span className="mono">{it.name}</span>
                    <button type="button"
                      className="reg-del"
                      title={t('registry.delete')}
                      onClick={(e) => {
                        e.stopPropagation()
                        setConfirmDel(it)
                      }}
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="card card-flush">
            <div style={{ padding: '10px 14px', borderBottom: '1px solid var(--border)', fontSize: 'var(--fs-sm)', color: 'var(--text-dim)' }}>
              {t('registry.value')}
            </div>
            {readValue ? (
              <div className="reg-value" style={{ border: 'none', marginTop: 0 }}>
                <div className="reg-value-header">
                  <span className="mono">{readValue.path}\\{readValue.key}</span>
                </div>
                <pre>{readValue.value}</pre>
              </div>
            ) : (
              <div className="empty">{t('registry.empty')}</div>
            )}
          </div>
        </div>
      )}

      <div style={{ marginTop: 14, display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <input
          type="text"
          className="field"
          placeholder={t('registry.key')}
          value={writeKey}
          onChange={(e) => setWriteKey(e.target.value)}
          style={{
            padding: '6px 10px',
            borderRadius: 'var(--radius)',
            border: '1px solid var(--border-strong)',
            background: 'var(--surface-soft)',
            color: 'var(--text)',
            fontSize: 'var(--fs-base)',
            flex: 1,
            minWidth: 120,
          }}
        />
        <input
          type="text"
          className="field"
          placeholder={t('registry.value')}
          value={writeValue}
          onChange={(e) => setWriteValue(e.target.value)}
          style={{
            padding: '6px 10px',
            borderRadius: 'var(--radius)',
            border: '1px solid var(--border-strong)',
            background: 'var(--surface-soft)',
            color: 'var(--text)',
            fontSize: 'var(--fs-base)',
            flex: 2,
            minWidth: 160,
          }}
        />
        <select
          value={writeType}
          onChange={(e) => setWriteType(e.target.value)}
          style={{
            padding: '6px 10px',
            borderRadius: 'var(--radius)',
            border: '1px solid var(--border-strong)',
            background: 'var(--surface-soft)',
            color: 'var(--text)',
            fontSize: 'var(--fs-base)',
            flexShrink: 0,
          }}
        >
          <option value="string">String</option>
          <option value="dword">DWORD</option>
          <option value="qword">QWORD</option>
        </select>
        <button type="button"
          className="btn sm primary"
          onClick={() => write(writeKey, writeValue)}
          disabled={!writeKey}
        >
          {t('registry.write')}
        </button>
      </div>
      <ConfirmDialog
        open={!!confirmDel}
        title={t(confirmDel?.isKey ? 'registry.confirmDeleteKey' : 'registry.confirmDeleteValue')}
        danger
        busy={deleting}
        confirmLabel={t('registry.delete')}
        onConfirm={() => confirmDel && del(confirmDel)}
        onCancel={() => setConfirmDel(null)}
      >
        <p>
          {confirmDel
            ? t(confirmDel.isKey ? 'registry.confirmDeleteKeyBody' : 'registry.confirmDeleteValueBody', {
                name: confirmDel.name,
                path: path || hive,
              })
            : ''}
        </p>
      </ConfirmDialog>
    </div>
  )
}
