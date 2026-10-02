import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { wsUrl } from '../lib/api'
import { encodeFrame, decodeFrame, WS_MSG_DATA, WS_MSG_RESIZE, WS_MSG_CLOSE, WS_MSG_FATAL } from '../lib/terminal'
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
  // Which shell to ask the implant for. Empty means "let the implant choose",
  // which is the behaviour that existed before this control. It is offered
  // because the default is not always the one that works -- a Windows target
  // whose PowerShell never becomes interactive gives a blank terminal, and the
  // only way to tell that from a quiet host is to try cmd.exe instead.
  const [shell, setShell] = useState('')
  // Which terminal implementation to use. They trade interaction against the
  // number of assumptions made about the target, so the choice belongs to the
  // operator:
  //
  //   shell       a real shell over a tunnel (default, best interaction)
  //   shell-copy  the same shell copied to a temp directory first, for hosts
  //               whose policy allows execution from temp but not System32
  //   exec        no shell at all -- each line is run directly. Loses pipes,
  //               redirection and builtins, but works where no shell exists
  const [mode, setMode] = useState('shell')

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
    // Set when the server reported a fatal end. The socket close that follows
    // must not be read as a dropped connection, or the terminal reopens.
    let finished = false
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
      if (stopped || finished || reconnectScheduled) return
      reconnectScheduled = true
      setConnected(false)
      const delay = Math.min(RECONNECT_BASE_MS * 2 ** attempts, RECONNECT_MAX_MS)
      attempts += 1
      term.writeln(t('terminal.reconnecting'))
      reconnectTimer = window.setTimeout(connect, delay)
    }

    const connect = () => {
      if (stopped || finished) return
      reconnectScheduled = false
      // The shell rides on the query string because it is chosen per connection,
      // not per session: switching shells reconnects without disturbing the
      // session or any other terminal open on it.
      const params = new URLSearchParams()
      if (mode && mode !== 'shell') params.set('mode', mode)
      if (shell) params.set('shell', shell)
      const qs = params.toString()
      const q = qs ? `?${qs}` : '' 
      const ws = new WebSocket(wsUrl(`/ws/sessions/${id}/terminal${q}`))
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
        } else if (frame.type === WS_MSG_FATAL) {
          // The server said this terminal cannot continue. Print why, mark the
          // terminal as finished and stop: reconnecting would re-send the same
          // request and fail the same way.
          if (frame.payload.length > 0) {
            term.writeln(new TextDecoder().decode(frame.payload))
          }
          term.writeln(t('terminal.endedMsg'))
          finished = true
          setConnected(false)
        } else if (frame.type === WS_MSG_CLOSE) {
          if (frame.payload.length > 0) {
            term.writeln(new TextDecoder().decode(frame.payload))
          }
          scheduleReconnect()
        }
      }
      ws.onclose = () => {
        if (stopped || finished) return
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
    // `mode` belongs here as much as `shell` does. Both are sent as query
    // parameters on connect, so changing the mode has to open a new socket; it
    // was missing from the list, which meant selecting shell-copy or exec left
    // the running shell connection in place and the choice had no effect until
    // something else forced a reconnect.
  }, [id, shell, mode])

  return (
    <div className="page page-terminal">
      <div className="page-header">
        <div>
          <div className="page-title">{t('terminal.title')}</div>
          <div className="page-sub">{t('terminal.session', { id })}</div>
        </div>
        <div className="toolbar">
          <label className="terminal-shell">
            <span>{t('terminal.mode')}</span>
            <select
              value={mode}
              onChange={(e) => setMode(e.target.value)}
              title={t('terminal.modeHint')}
            >
              <option value="shell">{t('terminal.modeShell')}</option>
              <option value="shell-copy">{t('terminal.modeShellCopy')}</option>
              <option value="exec">{t('terminal.modeExec')}</option>
            </select>
          </label>
          <label className="terminal-shell">
            <span>{t('terminal.shell')}</span>
            <select
              value={shell}
              onChange={(e) => setShell(e.target.value)}
              title={t('terminal.shellHint')}
            >
              <option value="">{t('terminal.shellDefault')}</option>
              <option value="cmd">cmd</option>
              <option value="powershell">powershell</option>
              <option value="pwsh">pwsh</option>
              <option value="sh">sh</option>
              <option value="bash">bash</option>
            </select>
          </label>
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
