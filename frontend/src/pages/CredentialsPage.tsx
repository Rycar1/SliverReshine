import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { VaultCredential } from '../lib/types'
import EmptyState from '../components/common/EmptyState'
import ConfirmDialog from '../components/common/ConfirmDialog'
import { useToast } from '../components/common/Toast'
import './pages.css'

/** Sliver's hash type enum, as exposed by the vault API. The labels follow the
 *  server's own naming so a value copied from the TUI means the same here. */
const HASH_TYPES: { value: number; label: string }[] = [
  { value: 0, label: 'INVALID' },
  { value: 100, label: 'MD5' },
  { value: 200, label: 'SHA1' },
  { value: 300, label: 'SHA2-256' },
  { value: 400, label: 'SHA2-512' },
  { value: 500, label: 'NTLM' },
  { value: 1000, label: 'NTLMv1' },
  { value: 1100, label: 'NTLMv2' },
  { value: 1200, label: 'NetNTLMv1' },
  { value: 1300, label: 'NetNTLMv2' },
  { value: 1400, label: 'NTLMv2-SSP' },
  { value: 5500, label: 'NetNTLMv1-SSP' },
  { value: 5600, label: 'NetNTLMv2-SSP' },
  { value: 1700, label: 'SHA2-512 (crypt)' },
  { value: 1800, label: 'SHA2-512 (unix)' },
  { value: 5000, label: 'SHA1 (crypt)' },
  { value: 7400, label: 'sha256crypt' },
  { value: 13711, label: 'VNC' },
  { value: 22000, label: 'WPA-PBKDF2-PMKID' },
]

function hashTypeLabel(v: number): string {
  const hit = HASH_TYPES.find((h) => h.value === v)
  return hit ? hit.label : String(v)
}

export default function CredentialsPage() {
  const { t } = useTranslation()
  const toast = useToast()

  const [creds, setCreds] = useState<VaultCredential[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState('')
  const [pendingDelete, setPendingDelete] = useState<VaultCredential | null>(null)

  // Add form
  const [showAdd, setShowAdd] = useState(false)
  const [form, setForm] = useState({
    username: '',
    plaintext: '',
    hash: '',
    hashType: 1000,
    collection: '',
  })
  const [sniffing, setSniffing] = useState(false)

  const load = useCallback(() => {
    api
      .creds()
      .then((d) => {
        setCreds(d.credentials || [])
        setError('')
      })
      .catch((e) => setError((e as Error).message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const submit = async () => {
    if (!form.username && !form.hash && !form.plaintext) {
      toast.push('error', t('creds.needSomething'))
      return
    }
    try {
      await api.credsAdd(form)
      toast.push('success', t('creds.added'))
      setForm({ username: '', plaintext: '', hash: '', hashType: 1000, collection: '' })
      setShowAdd(false)
      load()
    } catch (e) {
      toast.push('error', (e as Error).message)
    }
  }

  /** Ask the server to fingerprints the hash so the operator does not have to
   *  know whether they are holding NTLM, NetNTLMv2 or something else. */
  const sniff = async () => {
    if (!form.hash) {
      toast.push('error', t('creds.needHash'))
      return
    }
    setSniffing(true)
    try {
      const res = await api.credsSniff(form.hash)
      setForm((f) => ({ ...f, hashType: res.HashType }))
      toast.push('success', t('creds.sniffed', { type: hashTypeLabel(res.HashType) }))
    } catch (e) {
      toast.push('error', (e as Error).message)
    } finally {
      setSniffing(false)
    }
  }

  const remove = async (cred: VaultCredential) => {
    try {
      await api.credsRemove([cred.ID])
      toast.push('success', t('creds.removed'))
      setPendingDelete(null)
      load()
    } catch (e) {
      toast.push('error', (e as Error).message)
      setPendingDelete(null)
    }
  }

  const filtered = creds.filter((c) => {
    if (!query) return true
    const q = query.toLowerCase()
    return (
      c.Username.toLowerCase().includes(q) ||
      c.Hash.toLowerCase().includes(q) ||
      c.Plaintext.toLowerCase().includes(q) ||
      c.Collection.toLowerCase().includes(q)
    )
  })

  const cracked = creds.filter((c) => c.IsCracked || c.Plaintext).length

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>{t('creds.title')}</h1>
          <p className="muted">
            {t('creds.sub', { total: creds.length, cracked })}
          </p>
        </div>
        <div className="row">
          <input
            className="input"
            placeholder={t('creds.search')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label={t('creds.search')}
          />
          <button className="btn primary" onClick={() => setShowAdd((v) => !v)}>
            {showAdd ? t('common.cancel') : t('creds.add')}
          </button>
        </div>
      </div>

      {error && <div className="alert error">{error}</div>}

      {showAdd && (
        <div className="card">
          <h2>{t('creds.addTitle')}</h2>
          <p className="muted">{t('creds.addSub')}</p>
          <div className="grid2">
            <label>
              <span>{t('creds.username')}</span>
              <input
                className="input"
                value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value })}
              />
            </label>
            <label>
              <span>{t('creds.collection')}</span>
              <input
                className="input"
                placeholder={t('creds.collectionHint')}
                value={form.collection}
                onChange={(e) => setForm({ ...form, collection: e.target.value })}
              />
            </label>
            <label>
              <span>{t('creds.plaintext')}</span>
              <input
                className="input"
                value={form.plaintext}
                onChange={(e) => setForm({ ...form, plaintext: e.target.value })}
              />
            </label>
            <label>
              <span>{t('creds.hashType')}</span>
              <select
                className="input"
                value={form.hashType}
                onChange={(e) => setForm({ ...form, hashType: Number(e.target.value) })}
              >
                {HASH_TYPES.map((h) => (
                  <option key={h.value} value={h.value}>
                    {h.label}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <label>
            <span>{t('creds.hash')}</span>
            <textarea
              className="input mono"
              rows={2}
              value={form.hash}
              onChange={(e) => setForm({ ...form, hash: e.target.value })}
            />
          </label>
          <div className="row">
            <button className="btn" onClick={sniff} disabled={sniffing}>
              {sniffing ? t('creds.sniffing') : t('creds.sniff')}
            </button>
            <button className="btn primary" onClick={submit}>
              {t('common.save')}
            </button>
          </div>
        </div>
      )}

      <div className="card">
        {loading ? (
          <p className="muted">{t('common.loading')}</p>
        ) : filtered.length === 0 ? (
          <EmptyState
            title={t('creds.empty')}
            subtitle={t('creds.emptySub')}
          />
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>{t('creds.username')}</th>
                <th>{t('creds.secret')}</th>
                <th>{t('creds.hashType')}</th>
                <th>{t('creds.collection')}</th>
                <th>{t('creds.origin')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {filtered.map((c) => (
                <tr key={c.ID}>
                  <td>{c.Username || '-'}</td>
                  <td className="mono break">
                    {c.Plaintext ? (
                      <span className="badge ok">{c.Plaintext}</span>
                    ) : (
                      <span title={c.Hash}>{c.Hash || '-'}</span>
                    )}
                  </td>
                  <td>
                    <span className="badge">{hashTypeLabel(c.HashType)}</span>
                  </td>
                  <td>{c.Collection || '-'}</td>
                  <td className="mono muted small">
                    {c.OriginHostUUID ? c.OriginHostUUID.slice(0, 8) : '-'}
                  </td>
                  <td className="right">
                    <button
                      className="btn danger sm"
                      onClick={() => setPendingDelete(c)}
                      aria-label={t('common.delete')}
                    >
                      {t('common.delete')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <ConfirmDialog
        open={pendingDelete !== null}
        title={t('creds.deleteTitle')}
        danger
        onConfirm={() => pendingDelete && remove(pendingDelete)}
        onCancel={() => setPendingDelete(null)}
      >
        {t('creds.deleteMsg', { name: pendingDelete?.Username || pendingDelete?.ID || '' })}
      </ConfirmDialog>
    </div>
  )
}
