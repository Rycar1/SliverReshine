import type { ReactNode } from 'react'

export type StatusTone = 'green' | 'red' | 'yellow' | 'blue' | 'orange' | 'gray'

const TONE_CLASS: Record<StatusTone, string> = {
  green: 'badge green',
  red: 'badge red',
  yellow: 'badge yellow',
  blue: 'badge blue',
  orange: 'badge orange',
  gray: 'badge gray',
}

interface Props {
  tone: StatusTone
  children: ReactNode
  dot?: boolean
  title?: string
}

export default function StatusBadge({ tone, children, dot = false, title }: Props) {
  return (
    <span className={`${TONE_CLASS[tone]}${dot ? ' dot' : ''}`} title={title}>
      {children}
    </span>
  )
}
