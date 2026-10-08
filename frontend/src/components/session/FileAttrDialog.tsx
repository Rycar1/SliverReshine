import { useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { useToast } from '../common/Toast'

/** Which attribute the dialog was opened for. */
export type AttrKind = 'owner' | 'perm' | 'time'

// The permission levels icacls defines, in the order the backend validates
// them. The value is what goes on the command line; the label is what the
// operator reads.
const PERMS = ['F', 'M', 'RX', 'R', 'W'] as const

/**
 * FileAttrDialog edits one file's owner, permissions or timestamps.
 *
 * It is opened from the "More" menu on a row of the files tab, so the path is
 * already known and the operator never types one: the listing they clicked is
 * the listing the change applies to.
 *
 * The fields differ by platform because the underlying operation does. A POSIX
 * file has a uid/gid pair and a mode word; a Windows file has a security
 * descriptor, so ownership is an account name and permissions are a single
 * level granted to an account -- Sliver's chmod/chown RPCs have no Windows
 * handler at all, so those two go through icacls on the target instead.
 */
export default function FileAttrDialog({
  sessionId,
  os,
  path,
  name,
  kind,
  onClose,
  onDone,
}: {
  sessionId: string
  os?: string
  path: string
  name: string
  kind: AttrKind
  onClose: () => void
  onDone: () => void
}) {
  const { t } = useTranslation()
  const toast = useToast()
  const win = os === 'windows'

  const [busy, setBusy] = useState(false)
  const [recursive, setRecursive] = useState(false)
  const [account, setAccount] = useState(win ? 'Administrators' : '')
  const [uid, setUid] = useState('0')
  const [gid, setGid] = useState('0')
  const [principal, setPrincipal] = useState(win ? 'Administrators' : '')
  const [perm, setPerm] = useState<string>('F')
  const [mode, setMode] = useState('0755')
  const [atime, setAtime] = useState('')
  const [mtime, setMtime] = useState('')

  const action =
    kind === 'owner'
      ? t('files.attrOwner')
      : kind === 'perm'
        ? t('files.attrPerm')
        : t('files.attrTime')

  const apply = async () => {
    setBusy(true)
    try {
      if (kind === 'owner') {
        // A Windows file has one owner SID, so the gid half of chown has no
        // counterpart there and is left empty.
        await api.chown(sessionId, path, win ? account : uid, win ? '' : gid, recursive)
      } else if (kind === 'perm') {
        if (win) {
          await api.fsAcl(sessionId, path, principal, perm, recursive)
        } else {
          await api.chmod(sessionId, path, mode, recursive)
        }
      } else {
        // Empty means "now", which is what normalising a timestamp after a
        // modification usually wants.
        const now = Math.floor(Date.now() / 1000)
        await api.chtimes(
          sessionId,
          path,
          atime ? Number(atime) : now,
          mtime ? Number(mtime) : now,
        )
      }
      toast.push('success', t('files.attrDone'))
      onDone()
    } catch (e) {
      toast.push('error', (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return createPortal(
    <div className="modal-overlay confirm-overlay" onClick={onClose}>
      <div
        className="modal confirm-dialog"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={action}
      >
        <div className="modal-header">
          <div className="modal-title">{t('files.attrTitle', { action, name })}</div>
        </div>
        <div className="modal-body">
          {kind === 'owner' && win && (
            <>
              <div className="env-form">
                <input
                  type="text"
                  aria-label={t('files.attrOwnerAccount')}
                  placeholder={t('files.attrOwnerAccount')}
                  value={account}
                  onChange={(e) => setAccount(e.target.value)}
                  style={{ flex: 1, minWidth: 220 }}
                />
              </div>
              <p className="muted small">{t('files.attrOwnerAccountHint')}</p>
            </>
          )}

          {kind === 'owner' && !win && (
            <>
              <div className="env-form">
                <input
                  type="text"
                  aria-label={t('files.attrUid')}
                  placeholder={t('files.attrUid')}
                  value={uid}
                  onChange={(e) => setUid(e.target.value)}
                  style={{ width: 120 }}
                />
                <input
                  type="text"
                  aria-label={t('files.attrGid')}
                  placeholder={t('files.attrGid')}
                  value={gid}
                  onChange={(e) => setGid(e.target.value)}
                  style={{ width: 120 }}
                />
              </div>
              <p className="muted small">{t('files.attrOwnerUnixHint')}</p>
            </>
          )}

          {kind === 'perm' && win && (
            <>
              <div className="env-form">
                <input
                  type="text"
                  aria-label={t('files.attrPermAccount')}
                  placeholder={t('files.attrPermAccount')}
                  value={principal}
                  onChange={(e) => setPrincipal(e.target.value)}
                  style={{ flex: 1, minWidth: 200 }}
                />
                <select
                  aria-label={t('files.attrPermLevel')}
                  value={perm}
                  onChange={(e) => setPerm(e.target.value)}
                >
                  {PERMS.map((p) => (
                    <option key={p} value={p}>
                      {t(`files.perm${p}`)}
                    </option>
                  ))}
                </select>
              </div>
              <p className="muted small">{t('files.attrPermLevelHint')}</p>
            </>
          )}

          {kind === 'perm' && !win && (
            <>
              <div className="env-form">
                <input
                  type="text"
                  aria-label={t('files.attrMode')}
                  placeholder={t('files.attrMode')}
                  value={mode}
                  onChange={(e) => setMode(e.target.value)}
                  style={{ width: 140 }}
                />
              </div>
              <p className="muted small">{t('files.attrPermUnixHint')}</p>
            </>
          )}

          {kind === 'time' && (
            <>
              <div className="env-form">
                <input
                  type="text"
                  aria-label={t('files.attrAtime')}
                  placeholder={t('files.attrAtime')}
                  value={atime}
                  onChange={(e) => setAtime(e.target.value)}
                  style={{ flex: 1, minWidth: 180 }}
                />
                <input
                  type="text"
                  aria-label={t('files.attrMtime')}
                  placeholder={t('files.attrMtime')}
                  value={mtime}
                  onChange={(e) => setMtime(e.target.value)}
                  style={{ flex: 1, minWidth: 180 }}
                />
              </div>
              <p className="muted small">{t('files.attrTimeHint')}</p>
            </>
          )}

          {win && <p className="muted small">{t('files.attrWindowsHint')}</p>}

          {kind !== 'time' && (
            <label className="inline-check">
              <input
                type="checkbox"
                checked={recursive}
                onChange={(e) => setRecursive(e.target.checked)}
              />
              {t('files.attrRecursive')}
            </label>
          )}
        </div>
        <div className="modal-footer">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            {t('common.cancel')}
          </button>
          <button type="button" className="btn primary" onClick={apply} disabled={busy}>
            {busy ? t('common.working') : t('files.attrApply')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
