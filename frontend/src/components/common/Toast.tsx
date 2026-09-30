import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'

export type ToastTone = 'info' | 'success' | 'error' | 'warning'

export interface Toast {
  id: number
  tone: ToastTone
  message: string
}

interface ToastContextValue {
  push: (tone: ToastTone, message: string) => void
}

const ToastContext = createContext<ToastContextValue | null>(null)

const NOOP: ToastContextValue = { push: () => {} }

export function useToast(): ToastContextValue {
  return useContext(ToastContext) ?? NOOP
}

let nextId = 1

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>())

  const remove = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
    const timer = timers.current.get(id)
    if (timer) {
      clearTimeout(timer)
      timers.current.delete(id)
    }
  }, [])

  const push = useCallback(
    (tone: ToastTone, message: string) => {
      const id = nextId++
      setToasts((prev) => [...prev.slice(-4), { id, tone, message }])
      timers.current.set(
        id,
        setTimeout(() => remove(id), 4200),
      )
    },
    [remove],
  )

  return (
    <ToastContext.Provider value={{ push }}>
      {children}
      <div className="toast-region" role="status" aria-live="polite">
          {toasts.map((toast) => (
            <div key={toast.id} className={`toast toast-${toast.tone}`} onClick={() => remove(toast.id)}>
              <span className="toast-dot" />
              <span className="toast-message">{toast.message}</span>
              <button
                type="button"
                className="toast-close"
                aria-label="Close notification"
                onClick={(e) => {
                  e.stopPropagation()
                  remove(toast.id)
                }}
              >
                ×
              </button>
            </div>
          ))}
      </div>
    </ToastContext.Provider>
  )
}
