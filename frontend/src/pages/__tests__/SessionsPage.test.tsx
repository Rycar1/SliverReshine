import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import SessionsPage from '../../pages/SessionsPage'
import { api } from '../../lib/api'

vi.mock('../../lib/api', () => ({
  api: {
    sessions: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

describe('SessionsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders session rows from api', async () => {
    mockedApi.sessions.mockResolvedValue({
      sessions: [
        {
          ID: 'abc123',
          Name: 'SESS-1',
          UUID: 'uuid-1',
          Hostname: 'victim-host',
          Username: 'root',
          UID: '0',
          GID: '0',
          PID: 1337,
          OS: 'linux',
          Arch: 'amd64',
          Transport: 'mtls',
          RemoteAddress: '10.0.0.5:4242',
          LastCheckin: '2024-01-02T03:04:05Z',
          ActiveC2: 'mtls://1.2.3.4:8888',
          Locale: '',
          AgentVersion: '1.5.30',
          IsDead: false,
          IsInteractive: true,
        },
      ],
    })

    render(
      <MemoryRouter>
        <SessionsPage />
      </MemoryRouter>,
    )

    await waitFor(() => expect(screen.getByText('SESS-1')).toBeInTheDocument())
    expect(screen.getByText('victim-host')).toBeInTheDocument()
    expect(screen.getByText('root')).toBeInTheDocument()
    expect(screen.getByText('10.0.0.5:4242')).toBeInTheDocument()
  })

  it('shows empty state when no sessions', async () => {
    mockedApi.sessions.mockResolvedValue({ sessions: [] })

    render(
      <MemoryRouter>
        <SessionsPage />
      </MemoryRouter>,
    )

    await waitFor(() => expect(screen.getByText('No active sessions')).toBeInTheDocument())
  })

  it('shows error banner when api fails', async () => {
    mockedApi.sessions.mockRejectedValue(new Error('connection refused'))

    render(
      <MemoryRouter>
        <SessionsPage />
      </MemoryRouter>,
    )

    await waitFor(() => expect(screen.getByText('connection refused')).toBeInTheDocument())
  })
})
