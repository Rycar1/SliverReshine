import { useEffect, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

interface Props {
  open: boolean
  title: string
  children?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export default function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  cancelLabel,
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: Props) {
  const { t } = useTranslation()
  const okRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (open) okRef.current?.focus()
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onCancel()
      } else if (e.key === 'Enter') {
        e.preventDefault()
        if (!busy) onConfirm()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, busy, onConfirm, onCancel])

  if (!open) return null

  return (
    <div className="modal-overlay confirm-overlay" onClick={onCancel}>
      <div
        className="modal confirm-dialog"
        onClick={(e) => e.stopPropagation()}
        role="alertdialog"
        aria-modal="true"
        aria-label={title}
      >
        <div className="modal-header">
          <div className="modal-title">{title}</div>
        </div>
        <div className="modal-body">{children}</div>
        <div className="modal-footer">
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            {cancelLabel || t('common.cancel')}
          </button>
          <button type="button"
            ref={okRef}
            className={`btn ${danger ? 'danger' : 'primary'}`}
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? t('common.working') : confirmLabel || t('common.confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}
