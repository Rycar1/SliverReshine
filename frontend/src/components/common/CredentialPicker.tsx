import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { VaultCredential } from '../../lib/types'

/**
 * A credential the vault can supply to an operation.
 *
 * The vault stores hashes as well as plaintext, and most operations need a
 * password rather than an NT hash, so the picker reports which of the two it
 * can fill. Callers that only accept a password pass `requirePlaintext` and the
 * hash-only entries are filtered out rather than offered and then rejected.
 */
export interface PickedCredential {
  username: string
  secret: string
  domain: string
  /** The vault row this came from, so a caller can trace it. */
  id: string
}

interface Props {
  /**
   * Called whenever the selection changes. `null` means "nothing picked", which
   * includes the case where the vault is empty -- a caller must still work when
   * the operator types the values by hand.
   */
  onChange: (cred: PickedCredential | null) => void
  /** Hide hash-only entries, for operations that need a cleartext password. */
  requirePlaintext?: boolean
  /** Optional label override; the picker renders its own label by default. */
  label?: string
}

/**
 * CredentialPicker lets an operation be filled from the vault instead of by
 * retyping a password the operator already has.
 *
 * It fetches on mount rather than requiring the parent to pass a list, so it can
 * be dropped into any form without threading state through. A fetch failure is
 * reported inline and leaves the surrounding form fully usable: the vault is a
 * convenience, and a broken one must not block the manual path.
 */
export default function CredentialPicker({ onChange, requirePlaintext, label }: Props) {
  const { t } = useTranslation()
  const [creds, setCreds] = useState<VaultCredential[]>([])
  const [selected, setSelected] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
		const d = await api.creds()
		setCreds(d.credentials || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const usable = useMemo(() => {
    if (!requirePlaintext) return creds
    return creds.filter((c) => (c.Plaintext || '').trim() !== '')
  }, [creds, requirePlaintext])

  // A selection that is no longer in the list (the vault changed under us) is
  // cleared, so the form cannot submit a credential that has been deleted.
  useEffect(() => {
    if (selected && !usable.some((c) => c.ID === selected)) {
      setSelected('')
      onChange(null)
    }
  }, [usable, selected, onChange])

  const select = (id: string) => {
    setSelected(id)
    const found = usable.find((c) => c.ID === id)
    if (!found) {
      onChange(null)
      return
    }
    onChange({
      id: found.ID,
      username: found.Username,
      secret: found.Plaintext || found.Hash,
      domain: '',
    })
  }

  const describe = (c: VaultCredential) => {
    const secret = c.Plaintext ? t('creds.plaintext') : t('creds.hash')
    const parts = [c.Username || t('creds.username'), secret]
    if (c.Collection) parts.push(c.Collection)
    return parts.join(' · ')
  }

  return (
    <div className="cred-picker">
      <label>{label || t('creds.pickLabel')}</label>
      <div className="cred-picker-row">
        <select
          className="input"
          value={selected}
          onChange={(e) => select(e.target.value)}
          disabled={loading || usable.length === 0}
        >
          <option value="">
            {loading
              ? t('common.loading')
              : usable.length === 0
                ? t('creds.pickEmpty')
                : t('creds.pickPlaceholder')}
          </option>
          {usable.map((c) => (
            <option key={c.ID} value={c.ID}>
              {describe(c)}
            </option>
          ))}
        </select>
        <button type="button" className="btn subtle sm" onClick={load} disabled={loading}>
          {t('creds.pickRefresh')}
        </button>
      </div>
      {requirePlaintext && creds.length > usable.length && (
        <p className="muted cred-picker-note">
          {t('creds.pickHashHidden', { count: creds.length - usable.length })}
        </p>
      )}
      {error && <p className="muted cred-picker-note">{t('creds.pickError', { error })}</p>}
    </div>
  )
}
