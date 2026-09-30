import { useEffect, useRef, type ReactNode } from 'react'

export interface ContextMenuItem {
  label: string
  icon?: ReactNode
  danger?: boolean
  disabled?: boolean
  hint?: string
  onSelect?: () => void
}

interface Props {
  x: number
  y: number
  items: ContextMenuItem[]
  onClose: () => void
}

export default function ContextMenu({ x, y, items, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('mousedown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('mousedown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [onClose])

  const style: React.CSSProperties = {
    position: 'fixed',
    top: y,
    left: x,
    zIndex: 90,
  }

  return (
    <div ref={ref} className="context-menu" style={style} role="menu">
      {items.map((item, i) =>
        item.label === '-' ? (
          <div key={i} className="context-menu-sep" />
        ) : (
          <button type="button"
            key={i}
            role="menuitem"
            className={`context-menu-item${item.danger ? ' danger' : ''}`}
            disabled={item.disabled}
            onClick={() => {
              onClose()
              item.onSelect?.()
            }}
          >
            {item.icon && <span className="context-menu-icon">{item.icon}</span>}
            <span className="context-menu-label">{item.label}</span>
            {item.hint && <span className="context-menu-hint">{item.hint}</span>}
          </button>
        ),
      )}
    </div>
  )
}
