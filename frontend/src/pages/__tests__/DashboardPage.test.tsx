import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import DashboardPage from '../../pages/DashboardPage'
import { ConnectionProvider } from '../../lib/connection'
import { api } from '../../lib/api'
import type { Session } from '../../lib/types'

vi.mock('../../lib/api', () => ({
  api: {
    info: vi.fn(),
    overview: vi.fn(),
    sessions: vi.fn(),
    jobs: vi.fn(),
    beacons: vi.fn().mockResolvedValue({ beacons: [] }),
    events: vi.fn().mockResolvedValue({ events: [] }),
  },
}))

const mockedApi = vi.mocked(api)

const session = {
  ID: 'session-1',
  UUID: 'uuid-1',
  Name: 'SESS-1',
  Hostname: 'host-1',
  Username: 'root',
  OS: 'linux',
  Arch: 'amd64',
  LastCheckin: new Date().toISOString(),
} as Session

function renderPage() {
  const view = render(
    <MemoryRouter>
      <ConnectionProvider>
        <DashboardPage />
      </ConnectionProvider>
    </MemoryRouter>,
  )
  return view
}

describe('DashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders counts and operational data when connected', async () => {
    mockedApi.info.mockResolvedValue({ version: '1.15.16', connected: true })
    mockedApi.overview.mockResolvedValue({
      counts: { sessions: 3, beacons: 2, jobs: 5, builders: 7, socks: 1 },
    })
    mockedApi.sessions.mockResolvedValue({ sessions: [session] })
    mockedApi.jobs.mockResolvedValue({
      jobs: [{ ID: 9, Name: 'mtls', Protocol: 'mtls', Port: 8888, Domains: [], JobControl: '' }],
    })

    const view = renderPage()
    await waitFor(() => {
      expect(screen.getAllByText('3').length).toBeGreaterThan(0)
      expect(screen.getByText('SESS-1')).toBeInTheDocument()
      expect(screen.getAllByText('mtls').length).toBeGreaterThan(0)
    })
    view.unmount()
  })

  it('stays connected when overview collection fails', async () => {
    mockedApi.info.mockResolvedValue({ version: '1.15.16', connected: true })
    mockedApi.overview.mockRejectedValue(new Error('overview timed out'))
    mockedApi.sessions.mockResolvedValue({ sessions: [] })
    mockedApi.jobs.mockResolvedValue({ jobs: [] })

    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Sliver C2 operations at a glance')).toBeInTheDocument())
    expect(screen.queryByText('Not connected to Sliver server')).not.toBeInTheDocument()
    view.unmount()
  })

  it('clears operational data when the server is disconnected', async () => {
    mockedApi.info.mockResolvedValue({ version: '', connected: false })
    mockedApi.overview.mockResolvedValue({
      counts: { sessions: 9, beacons: 0, jobs: 0, builders: 0, socks: 0 },
    })
    mockedApi.sessions.mockResolvedValue({ sessions: [session] })
    mockedApi.jobs.mockResolvedValue({ jobs: [] })

    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Not connected to Sliver server')).toBeInTheDocument())
    expect(screen.queryByText('SESS-1')).not.toBeInTheDocument()
    expect(mockedApi.overview).not.toHaveBeenCalled()
    view.unmount()
  })
})