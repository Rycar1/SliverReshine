import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

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
  // The menu is placed from a viewport coordinate, so it has to be measured
  // against the viewport. A caller inside the page tree sits under
  // `.page-transition`, and a `transform` on that chain -- from the route
  // animation, or from a `will-change` that promotes the layer -- makes the
  // ancestor the containing block for `position: fixed` and shifts the menu by
  // the ancestor's own origin. Portalling to the body takes the menu out of
  // that chain; the clamp keeps it on screen when the row it was opened from
  // is at the edge.
  const [pos, setPos] = useState({ x, y })
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const r = el.getBoundingClientRect()
    setPos({
      x: Math.max(8, Math.min(x, window.innerWidth - r.width - 8)),
      y: Math.max(8, Math.min(y, window.innerHeight - r.height - 8)),
    })
  }, [x, y])

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
    top: pos.y,
    left: pos.x,
    zIndex: 90,
  }

  return createPortal(
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
    </div>,
    document.body,
  )
}
