import type { ReactNode } from 'react'

interface Props {
  icon?: ReactNode
  title: string
  subtitle?: string
}

export default function EmptyState({ icon, title, subtitle }: Props) {
  return (
    <div className="empty-state">
      {icon && <div className="empty-state-icon">{icon}</div>}
      <div className="empty-state-title">{title}</div>
      {subtitle && <div className="empty-state-sub">{subtitle}</div>}
    </div>
  )
}
