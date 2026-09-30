import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

interface Props {
  value: string
  onSave: (next: string) => void
  mono?: boolean
}

export default function InlineEdit({ value, onSave, mono }: Props) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)
  const cancelRef = useRef(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing) {
      setDraft(value)
      cancelRef.current = false
      inputRef.current?.focus()
      inputRef.current?.select()
    }
  }, [editing, value])

  const finish = (cancel: boolean) => {
    setEditing(false)
    cancelRef.current = false
    if (cancel) return
    const next = draft.trim()
    if (next && next !== value) onSave(next)
  }

  if (!editing) {
    return (
      <button type="button"
        className={`inline-edit ${mono ? 'mono' : ''}`}
        onClick={() => setEditing(true)}
        title={t('common.rename')}
      >
        {value}
      </button>
    )
  }

  return (
    <input
      ref={inputRef}
      className="inline-edit-input"
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          finish(false)
        } else if (e.key === 'Escape') {
          finish(true)
        }
      }}
      onBlur={() => finish(cancelRef.current)}
    />
  )
}
