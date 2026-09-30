import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import SettingsPage from '../../pages/SettingsPage'
import { ConnectionProvider } from '../../lib/connection'
import { api } from '../../lib/api'
import i18n, { LANG_KEY } from '../../i18n'

vi.mock('../../lib/api', () => ({
  api: {
    info: vi.fn(),
    overview: vi.fn(),
    listProfiles: vi.fn(),
    connect: vi.fn(),
    disconnect: vi.fn(),
    useProfile: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

function renderPage() {
  return render(
    <MemoryRouter>
      <ConnectionProvider>
        <SettingsPage />
      </ConnectionProvider>
    </MemoryRouter>,
  )
}

describe('SettingsPage language option', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    localStorage.clear()
    await i18n.changeLanguage('en')
    mockedApi.info.mockResolvedValue({ connected: false, version: '' })
    mockedApi.overview.mockResolvedValue({
      counts: { sessions: 0, beacons: 0, jobs: 0, builders: 0, socks: 0 },
    })
    mockedApi.listProfiles.mockResolvedValue({ profiles: [] })
  })

  it('offers both shipped languages', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Language')).toBeInTheDocument())

    // 语言名用各自的母语书写（English / 中文），不随当前语言翻译。
    expect(screen.getByText('English')).toBeInTheDocument()
    expect(screen.getByText('中文')).toBeInTheDocument()
    view.unmount()
  })

  it('marks the active language as pressed', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Language')).toBeInTheDocument())

    expect(screen.getByText('English').closest('button')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByText('中文').closest('button')).toHaveAttribute('aria-pressed', 'false')
    view.unmount()
  })

  it('switches the UI to Chinese and persists the choice', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Language')).toBeInTheDocument())

    fireEvent.click(screen.getByText('中文'))

    // 切换后整个页面应立即变成中文，且选择写入 i18n 初始化时读取的同一个 key。
    await waitFor(() => expect(screen.getByText('界面语言')).toBeInTheDocument())
    expect(localStorage.getItem(LANG_KEY)).toBe('zh')
    expect(screen.getByText('中文').closest('button')).toHaveAttribute('aria-pressed', 'true')
    view.unmount()
  })

  it('switches back to English', async () => {
    await i18n.changeLanguage('zh')
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('界面语言')).toBeInTheDocument())

    fireEvent.click(screen.getByText('English'))

    await waitFor(() => expect(screen.getByText('Language')).toBeInTheDocument())
    expect(localStorage.getItem(LANG_KEY)).toBe('en')
    view.unmount()
  })
})
