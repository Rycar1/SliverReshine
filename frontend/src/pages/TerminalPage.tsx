import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { wsUrl } from '../lib/api'
import { encodeFrame, decodeFrame, WS_MSG_DATA, WS_MSG_RESIZE, WS_MSG_CLOSE } from '../lib/terminal'
import './pages.css'
import './terminal.css'

const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 5000

export default function TerminalPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const containerRef = useRef<HTMLDivElement>(null)
  const [connected, setConnected] = useState(false)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const { t } = useTranslation()

  useEffect(() => {
    if (!containerRef.current) return
    const term = new Terminal({
      fontSize: 13,
      fontFamily: 'JetBrains Mono, Fira Code, Consolas, monospace',
      cursorBlink: true,
      theme: {
        background: '#0a0a0b',
        foreground: '#d5dae5',
        cursor: '#5e6ad2',
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    const fitTerminal = () => {
      try {
        fit.fit()
      } catch {
        // container not laid out yet; retry on next frame
      }
    }
    fitTerminal()
    termRef.current = term
    fitRef.current = fit

    // Reconnect state. `stopped` flips on unmount so timers and in-flight
    // sockets are torn down; `attempts` drives exponential backoff.
    let stopped = false
    let attempts = 0
    let reconnectScheduled = false
    let reconnectTimer: number | undefined

    const sendResize = () => {
      if (!fitRef.current || !termRef.current) return
      try {
        fitRef.current.fit()
      } catch {
        return
      }
      const term = termRef.current
      if (
        term.cols > 0 &&
        term.rows > 0 &&
        !stopped &&
        wsRef.current &&
        wsRef.current.readyState === WebSocket.OPEN
      ) {
        wsRef.current.send(encodeFrame(WS_MSG_RESIZE, JSON.stringify({ cols: term.cols, rows: term.rows })))
      }
    }

    const scheduleReconnect = () => {
      if (stopped || reconnectScheduled) return
      reconnectScheduled = true
      setConnected(false)
      const delay = Math.min(RECONNECT_BASE_MS * 2 ** attempts, RECONNECT_MAX_MS)
      attempts += 1
      term.writeln(t('terminal.reconnecting'))
      reconnectTimer = window.setTimeout(connect, delay)
    }

    const connect = () => {
      if (stopped) return
      reconnectScheduled = false
      const ws = new WebSocket(wsUrl(`/ws/sessions/${id}/terminal`))
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws
      ws.onopen = () => {
        if (stopped) return
        attempts = 0
        setConnected(true)
        term.writeln(t('terminal.connectedMsg'))
        sendResize()
      }
      ws.onmessage = (e) => {
        if (stopped) return
        const frame = decodeFrame(e.data)
        if (!frame) return
        if (frame.type === WS_MSG_DATA) {
          term.write(frame.payload)
        } else if (frame.type === WS_MSG_CLOSE) {
          if (frame.payload.length > 0) {
            term.writeln(new TextDecoder().decode(frame.payload))
          }
          scheduleReconnect()
        }
      }
      ws.onclose = () => {
        if (stopped) return
        scheduleReconnect()
      }
      ws.onerror = () => {
        // onclose follows onerror; it handles the reconnect scheduling.
      }
    }

    const onData = (data: string) => {
      const ws = wsRef.current
      if (!stopped && ws && ws.readyState === WebSocket.OPEN)
        ws.send(encodeFrame(WS_MSG_DATA, data))
    }
    term.onData(onData)

    const onResize = sendResize
    window.addEventListener('resize', onResize)

    connect()
    sendResize()

    return () => {
      stopped = true
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      window.removeEventListener('resize', onResize)
      if (wsRef.current) wsRef.current.close()
      term.dispose()
    }
  }, [id])

  return (
    <div className="page page-terminal">
      <div className="page-header">
        <div>
          <div className="page-title">{t('terminal.title')}</div>
          <div className="page-sub">{t('terminal.session', { id })}</div>
        </div>
        <div className="toolbar">
          <span className={`badge ${connected ? 'green' : 'red'}`}>
            {connected ? t('terminal.connected') : t('terminal.disconnected')}
          </span>
          <button type="button" className="btn" onClick={() => navigate('/')}>
            {t('common.back')}
          </button>
        </div>
      </div>
      <div ref={containerRef} className="terminal-container" />
    </div>
  )
}
