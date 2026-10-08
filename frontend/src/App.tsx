import type { ReactNode } from 'react'
import { useEffect, useState, useRef, useCallback } from 'react'
import { Routes, Route, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ConnectionProvider, useConnection } from './lib/connection'
import { GROUPED_NAV, ICONS } from './lib/nav'
import { currentLang, setLang, type Lang } from './lib/lang'
import DashboardPage from './pages/DashboardPage'
import SessionsPage from './pages/SessionsPage'
import SessionDetailPage from './pages/SessionDetailPage'
import TerminalPage from './pages/TerminalPage'
import ListenersPage from './pages/ListenersPage'
import BeaconsPage from './pages/BeaconsPage'
import BeaconDetailPage from './pages/BeaconDetailPage'
import ImplantsPage from './pages/ImplantsPage'
import SocksPage from './pages/SocksPage'
import JobsPage from './pages/JobsPage'
import TasksPage from './pages/TasksPage'
import LootPage from './pages/LootPage'
import CanariesPage from './pages/CanariesPage'
import AliasesPage from './pages/AliasesPage'
import FilesPage from './pages/FilesPage'
import ProcessesPage from './pages/ProcessesPage'
import NetworkPage from './pages/NetworkPage'
import EventsPage from './pages/EventsPage'
import HostsPage from './pages/HostsPage'
import TopologyPage from './pages/TopologyPage'
import WebDeliveryPage from './pages/WebDeliveryPage'
import RawRpcPage from './pages/RawRpcPage'
import WebsitesPage from './pages/WebsitesPage'
import SettingsPage from './pages/SettingsPage'
import CredentialsPage from './pages/CredentialsPage'
import InfrastructurePage from './pages/InfrastructurePage'
import AIConfigPage from './pages/AIConfigPage'
import Logo from './components/Logo'
import { ToastProvider } from './components/common/Toast'
import './App.css'

export default function App() {
  return (
    <ToastProvider>
      <ConnectionProvider>
        <AppLayout />
      </ConnectionProvider>
    </ToastProvider>
  )
}

function AppLayout() {
  const { connected, version, counts, ready } = useConnection()
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const location = useLocation()

  // 侧栏折叠 — 宽屏手动切换,窄屏默认收起;持久化到 localStorage
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      const stored = localStorage.getItem('sliverreshine-sidebar')
      if (stored !== null) return stored === '1'
    } catch {
      /* ignore */
    }
    return typeof window !== 'undefined' && window.innerWidth < 900
  })
  useEffect(() => {
    localStorage.setItem('sliverreshine-sidebar', collapsed ? '1' : '0')
  }, [collapsed])

  // 语言状态与侧栏按钮 / 设置页共享同一套读写逻辑（见 lib/lang.ts）。
  const [language, setLanguage] = useState<Lang>(currentLang())
  useEffect(() => {
    const onChanged = (l: string) => setLanguage(l === 'zh' ? 'zh' : 'en')
    i18n.on('languageChanged', onChanged)
    setLanguage(currentLang())
    return () => {
      i18n.off('languageChanged', onChanged)
    }
  }, [])

  // 页面转场方向 — 深入=forward(右滑入),返回=back(左滑入),同级=same(微 fade)
  const prevPathRef = useRef(location.pathname)
  const prevDepth = prevPathRef.current.split('/').filter(Boolean).length
  const currDepth = location.pathname.split('/').filter(Boolean).length
  const pageDir = currDepth > prevDepth ? 'forward' : currDepth < prevDepth ? 'back' : 'same'
  useEffect(() => {
    prevPathRef.current = location.pathname
  }, [location.pathname])

  // 收藏列表 — localStorage 持久化,默认收藏 sessions / implants / beacons
  const FAV_KEY = 'sliverreshine-favorites'
  const DEFAULT_FAVS = ['sessions', 'implants', 'beacons']
  const [favorites, setFavorites] = useState<string[]>(() => {
    try {
      const stored = localStorage.getItem(FAV_KEY)
      return stored ? JSON.parse(stored) : DEFAULT_FAVS
    } catch {
      return DEFAULT_FAVS
    }
  })
  useEffect(() => {
    localStorage.setItem(FAV_KEY, JSON.stringify(favorites))
  }, [favorites])
  const toggleFav = useCallback((key: string) => {
    setFavorites((prev) =>
      prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key],
    )
  }, [])
  const ALL_NAV = GROUPED_NAV.flatMap((section) => section.items)
  const favoriteItems = ALL_NAV.filter((item) => favorites.includes(item.key))

  const badgeFor = (key: string): number | null => {
    switch (key) {
      case 'sessions':
        return counts.sessions
      case 'beacons':
        return counts.beacons
      case 'listeners':
        return counts.jobs
      case 'jobs':
        return counts.jobs
      case 'implants':
        return counts.builders
      case 'socks':
        return counts.socks
      default:
        return null
    }
  }

  const renderNavItem = (item: { path: string; key: string; icon: ReactNode; favorite?: boolean; disabled?: boolean }) => {
    const isFav = favorites.includes(item.key)
    const badge = badgeFor(item.key)
    const active =
      item.path === '/'
        ? location.pathname === '/'
        : location.pathname === item.path || location.pathname.startsWith(item.path + '/')
    if (item.disabled) {
      return (
        <div key={item.path + item.key} className="nav-item disabled">
          <span className="nav-icon">{item.icon}</span>
          <span className="nav-label">{t(`nav.${item.key}`)}</span>
        </div>
      )
    }
    return (
      <button type="button"
        key={item.path + item.key}
        className={`nav-item ${active ? 'active' : ''}`}
        onClick={() => navigate(item.path)}
        title={t(`nav.${item.key}`)}
      >
        <span className="nav-icon">{item.icon}</span>
        <span className="nav-label">{t(`nav.${item.key}`)}</span>
        {badge !== null && badge > 0 && <span className="nav-badge mono">{badge}</span>}
        <span
          className={`star-toggle ${isFav ? 'active' : ''}`}
          role="button"
          tabIndex={0}
          onClick={(e) => {
            e.stopPropagation()
            toggleFav(item.key)
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              e.stopPropagation()
              toggleFav(item.key)
            }
          }}
          aria-label={isFav ? t('nav.removeFav') : t('nav.addFav')}
          title={isFav ? t('nav.removeFav') : t('nav.addFav')}
        >
          {isFav ? ICONS.star : ICONS.starOutline}
        </span>
      </button>
    )
  }

  return (
    <div className={`app ${collapsed ? 'sidebar-collapsed' : ''}`}>
      <aside className="sidebar">
      <div className="logo">
        <span className="logo-mark">
          <Logo size={18} />
        </span>
        <span className="logo-text">{t('app.title')}</span>
        <span className="logo-caret">{ICONS.caret}</span>
      </div>

      <nav>
        {GROUPED_NAV.map((section) => (
          <div className="nav-section" key={section.key}>
            <div className="nav-section-title">{t(`nav.group.${section.key}`)}</div>
            {section.items.map(renderNavItem)}
          </div>
        ))}

          <div className="nav-section">
            <div className="nav-section-title">
              {t('app.favorites')}
              <span className="nav-section-count">{favoriteItems.length}</span>
            </div>
            {favoriteItems.length > 0 ? (
              favoriteItems.map(renderNavItem)
            ) : (
              <div className="nav-empty">{t('nav.favEmpty')}</div>
            )}
          </div>
        </nav>

        <div className="sidebar-footer">
          <div className="conn-status">
            <span className={`dot ${connected ? 'ok' : 'bad'}`} />
            <span>
              {!ready
                ? t('app.connecting')
                : connected
                  ? t('app.connected', { version })
                  : t('app.notConnected')}
            </span>
          </div>
          <button type="button" className="lang-switch" onClick={() => setLang(language === 'zh' ? 'en' : 'zh')} title={t('app.language')} aria-label={t('app.language')}>
            {ICONS.globe}
            <span>{language === 'zh' ? 'EN' : '中'}</span>
          </button>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <div className="topbar-left">
            <button type="button"
              className="btn icon sidebar-toggle"
              onClick={() => setCollapsed((c) => !c)}
              title={collapsed ? t('app.expand') : t('app.collapse')}
              aria-label={collapsed ? t('app.expand') : t('app.collapse')}
            >
              {ICONS.menu}
            </button>
            <div className="topbar-breadcrumb">
              <span className="topbar-ws">{t('app.workspace')}</span>
              <span className="topbar-sep">/</span>
              <span className="topbar-page">{t('app.title')}</span>
            </div>
          </div>
          <div className="topbar-right">
            <div className={`server-status ${ready ? (connected ? 'ok' : 'bad') : ''}`}>
              <span className={`dot ${connected ? 'ok' : 'bad'}`} />
              <span className="server-status-text">
                {!ready
                  ? t('status.connecting')
                  : connected
                    ? t('status.connected')
                    : t('status.disconnected')}
              </span>
            </div>
          </div>
        </header>
        <main className="content">
          {ready && !connected && (
            <div className="offline-banner">
              <span className="dot bad" />
              <span>{t('app.offline')}</span>
            </div>
          )}
          <div key={location.pathname} className={`page-transition ${pageDir}`}>
            <Routes location={location}>
              <Route path="/" element={<DashboardPage />} />
              <Route path="/sessions" element={<SessionsPage />} />
              <Route path="/beacons" element={<BeaconsPage />} />
              <Route path="/beacons/:id" element={<BeaconDetailPage />} />
              <Route path="/listeners" element={<ListenersPage />} />
              <Route path="/implants" element={<ImplantsPage />} />
              <Route path="/socks" element={<SocksPage />} />
              <Route path="/jobs" element={<JobsPage />} />
              <Route path="/tasks" element={<TasksPage />} />
              <Route path="/loot" element={<LootPage />} />
              <Route path="/canaries" element={<CanariesPage />} />
              <Route path="/aliases" element={<AliasesPage />} />
              <Route path="/rpc" element={<RawRpcPage />} />
              <Route path="/files" element={<FilesPage />} />
              <Route path="/processes" element={<ProcessesPage />} />
              <Route path="/network" element={<NetworkPage />} />
              <Route path="/events" element={<EventsPage />} />
              <Route path="/hosts" element={<HostsPage />} />
              <Route path="/topology" element={<TopologyPage />} />
              <Route path="/webdelivery" element={<WebDeliveryPage />} />
              <Route path="/websites" element={<WebsitesPage />} />
              <Route path="/ai" element={<AIConfigPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="/credentials" element={<CredentialsPage />} />
              <Route path="/infrastructure" element={<InfrastructurePage />} />
              <Route path="/sessions/:id" element={<SessionDetailPage />} />
              <Route path="/sessions/:id/terminal" element={<TerminalPage />} />
            </Routes>
          </div>
        </main>
      </div>
      </div>
  )
}
