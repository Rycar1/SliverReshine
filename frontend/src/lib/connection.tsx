import { createContext, useContext, useEffect, useState, useCallback, type ReactNode } from 'react'
import { api } from './api'
import type { OverviewCounts } from './types'

export const EMPTY_COUNTS: OverviewCounts = { sessions: 0, beacons: 0, jobs: 0, builders: 0, socks: 0 }

interface ConnectionValue {
  connected: boolean
  version: string
  counts: OverviewCounts
  ready: boolean
  degraded: boolean
  refresh: () => Promise<void>
}

const ConnectionContext = createContext<ConnectionValue>({
  connected: false,
  version: '',
  counts: EMPTY_COUNTS,
  ready: false,
  degraded: false,
  refresh: async () => {},
})

export function useConnection(): ConnectionValue {
  return useContext(ConnectionContext)
}

export function ConnectionProvider({ children, interval = 5000 }: { children: ReactNode; interval?: number }) {
  const [connected, setConnected] = useState(false)
  const [version, setVersion] = useState('')
  const [counts, setCounts] = useState<OverviewCounts>(EMPTY_COUNTS)
  const [ready, setReady] = useState(false)
  const [degraded, setDegraded] = useState(false)

  const refresh = useCallback(async () => {
    let current
    try {
      current = await api.info()
    } catch {
      // The request itself failed: the console is genuinely not talking to the
      // server.
      setConnected(false)
      setDegraded(false)
      setVersion('')
      setCounts(EMPTY_COUNTS)
      setReady(true)
      return
    }
    // A server that answers but reports an error is reachable and degraded, not
    // absent. Collapsing the two made a running server with an unreadable
    // version look like no server at all.
    setConnected(!!current.connected || !!current.error)
    setDegraded(!!current.error)
    setVersion(current.version || '')
    if (!current.connected) {
      setCounts(EMPTY_COUNTS)
      setReady(true)
      return
    }
    try {
      const ov = await api.overview()
      setCounts(ov.counts)
    } catch {
      // A timeout while collecting badges must not make the server appear disconnected.
    }
    setReady(true)
  }, [])

  useEffect(() => {
    refresh()
    const timer = setInterval(refresh, interval)
    return () => clearInterval(timer)
  }, [refresh, interval])

  return (
    <ConnectionContext.Provider value={{ connected, version, counts, ready, degraded, refresh }}>
      {children}
    </ConnectionContext.Provider>
  )
}
