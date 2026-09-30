import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { Session } from '../lib/types'
import InlineEdit from '../components/InlineEdit'
import DataTable, { type Column } from '../components/common/DataTable'
import StatusBadge from '../components/common/StatusBadge'
import ConfirmDialog from '../components/common/ConfirmDialog'
import ContextMenu from '../components/common/ContextMenu'
import EmptyState from '../components/common/EmptyState'
import { useToast } from '../components/common/Toast'
import './pages.css'

type TFunc = ReturnType<typeof useTranslation>['t']

function fmtTime(ts: string, t: TFunc): string {
  if (!ts) return '-'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const now = Date.now()
  const diff = Math.round((now - d.getTime()) / 1000)
  if (diff < 0) return t('time.justNow')
  if (diff < 60) return t('time.secondsAgo', { count: diff })
  if (diff < 3600) return t('time.minutesAgo', { count: Math.round(diff / 60) })
  if (diff < 86400) return t('time.hoursAgo', { count: Math.round(diff / 3600) })
  return t('time.daysAgo', { count: Math.round(diff / 86400) })
}

export default function SessionsPage() {
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [menu, setMenu] = useState<{ x: number; y: number; session: Session } | null>(null)
  const [killing, setKilling] = useState<Session | null>(null)
  const [busy, setBusy] = useState(false)
  const [selected, setSelected] = useState<Session | null>(null)
  const [pruning, setPruning] = useState(false)
  const navigate = useNavigate()
  const { t } = useTranslation()
  const toast = useToast()
  const loadRef = useRef<() => void>(() => {})

  const load = async () => {
    try {
      const data = await api.sessions()
      setSessions(data.sessions || [])
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }
  loadRef.current = load

  useEffect(() => {
    load()
    const timer = setInterval(load, 3000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = (e.target as HTMLElement)?.closest('input, textarea, select')
      if (typing) return
      const key = e.key.toLowerCase()
      if (key === 'r') {
        e.preventDefault()
        loadRef.current()
      } else if (key === 't' && selected) {
        e.preventDefault()
        navigate(`/sessions/${selected.ID}/terminal`)
      } else if (key === 'f' && selected) {
        e.preventDefault()
        navigate(`/sessions/${selected.ID}`)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [selected, navigate])

  const rename = async (s: Session, name: string) => {
    try {
      await api.renameSession(s.ID, name)
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const kill = async (s: Session) => {
    setBusy(true)
    try {
      await api.killSession(s.ID)
      toast.push('success', t('sessions.killed', { name: s.Name }))
      setKilling(null)
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const prune = async () => {
    setPruning(true)
    try {
      const res = await api.pruneSessions()
      toast.push('success', t('sessions.pruned', { count: res.pruned }))
      load()
    } catch (e) {
      toast.push('error', `${t('common.failed')}: ${(e as Error).message}`)
    } finally {
      setPruning(false)
    }
  }

  const columns: Column<Session>[] = [
    {
      key: 'Name',
      label: t('sessions.thName'),
      sortable: true,
      render: (s) => (
        <span className="mono">
          <InlineEdit value={s.Name} onSave={(name) => rename(s, name)} mono />
        </span>
      ),
    },
    {
      key: 'Type',
      label: t('sessions.thType'),
      sortable: true,
      render: () => <StatusBadge tone="blue">SESSION</StatusBadge>,
    },
    { key: 'Hostname', label: t('sessions.thHost'), sortable: true },
    { key: 'Username', label: t('sessions.thUser'), sortable: true },
    {
      key: 'OS',
      label: t('sessions.thOsArch'),
      sortable: true,
      render: (s) => (
        <span className="badge blue">
          {s.OS}/{s.Arch}
        </span>
      ),
    },
    {
      key: 'RemoteAddress',
      label: t('sessions.thIp'),
      sortable: true,
      render: (s) => <span className="mono">{s.RemoteAddress}</span>,
    },
    {
      key: 'PID',
      label: t('sessions.thPid'),
      sortable: true,
      render: (s) => <span className="mono">{s.PID}</span>,
    },
    {
      key: 'LastCheckin',
      label: t('sessions.thCheckin'),
      sortable: true,
      sortValue: (s) => new Date(s.LastCheckin).getTime(),
      render: (s) => fmtTime(s.LastCheckin, t),
    },
    {
      key: 'Status',
      label: t('sessions.thStatus'),
      sortable: true,
      render: (s) => (
        <StatusBadge tone={s.IsDead ? 'red' : 'green'} dot>
          {s.IsDead ? t('sessions.dead') : t('sessions.active')}
        </StatusBadge>
      ),
    },
  ]

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('sessions.title')}</div>
          <div className="page-sub">{t('sessions.sub', { count: sessions.length })}</div>
        </div>
        <div className="toolbar">
          <span className="kbd-hint mono">
            <kbd className="kbd">R</kbd> {t('common.refresh')}
            <kbd className="kbd">T</kbd> {t('common.terminal')}
            <kbd className="kbd">F</kbd> {t('sessions.open')}
          </span>
          <button type="button" className="btn" onClick={prune} disabled={pruning}>
            {pruning ? t('common.loading') : t('sessions.prune')}
          </button>
          <button type="button" className="btn" onClick={load}>
            {t('common.refresh')}
          </button>
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <div className="card card-flush">
        <DataTable
          columns={columns}
          rows={sessions}
          rowKey={(s) => s.UUID || s.ID}
          searchable
          searchPlaceholder={t('sessions.search')}
          searchText={(s) => `${s.Name} ${s.Hostname} ${s.Username} ${s.OS} ${s.RemoteAddress}`}
          loading={loading}
          empty={
            <EmptyState
              title={t('sessions.empty')}
              subtitle={t('sessions.emptySub')}
            />
          }
          onRowClick={(s) => navigate(`/sessions/${s.ID}`)}
          onRowDoubleClick={(s) => navigate(`/sessions/${s.ID}/terminal`)}
          onRowContextMenu={(e, s) => {
            e.preventDefault()
            setMenu({ x: e.clientX, y: e.clientY, session: s })
          }}
          onSelectedChange={setSelected}
          navigable
        />
      </div>
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
          items={[
            {
              label: t('sessions.open'),
              hint: 'F',
              onSelect: () => navigate(`/sessions/${menu.session.ID}`),
            },
            {
              label: t('common.terminal'),
              hint: 'T',
              onSelect: () => navigate(`/sessions/${menu.session.ID}/terminal`),
            },
            { label: '-' },
            {
              label: t('sessions.kill'),
              danger: true,
              onSelect: () => setKilling(menu.session),
            },
          ]}
        />
      )}
      <ConfirmDialog
        open={!!killing}
        title={t('sessions.confirmKill')}
        danger
        busy={busy}
        confirmLabel={t('sessions.kill')}
        onConfirm={() => killing && kill(killing)}
        onCancel={() => setKilling(null)}
      >
        <p>{killing ? t('sessions.confirmKillBody', { name: killing.Name, id: killing.ID }) : ''}</p>
      </ConfirmDialog>
    </div>
  )
}
