import { useEffect, type ReactNode } from 'react'

interface Props {
  open: boolean
  title: string
  subtitle?: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  width?: number
}

export default function DetailDrawer({ open, title, subtitle, onClose, children, footer, width = 420 }: Props) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  return (
    <div className={`drawer-overlay ${open ? 'open' : ''}`} onClick={onClose}>
      <aside className="drawer" style={{ width: `min(${width}px, 94vw)` }} onClick={(e) => e.stopPropagation()}>
        <div className="drawer-header">
          <div className="drawer-title-block">
            <div className="drawer-title">{title}</div>
            {subtitle && <div className="drawer-sub mono">{subtitle}</div>}
          </div>
          <button type="button" className="btn icon" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>
        <div className="drawer-body">{children}</div>
        {footer && <div className="drawer-footer">{footer}</div>}
      </aside>
    </div>
  )
}
