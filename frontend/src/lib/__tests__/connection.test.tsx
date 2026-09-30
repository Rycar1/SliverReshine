import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { ConnectionProvider, useConnection } from '../connection'
import { api } from '../api'

vi.mock('../api', () => ({
  api: {
    info: vi.fn(),
    overview: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

function Probe() {
  const { connected, version, counts, ready } = useConnection()
  return (
    <div>
      <span data-testid="connected">{String(connected)}</span>
      <span data-testid="version">{version}</span>
      <span data-testid="ready">{String(ready)}</span>
      <span data-testid="count-sessions">{counts.sessions}</span>
    </div>
  )
}

function renderProvider() {
  return render(
    <ConnectionProvider>
      <Probe />
    </ConnectionProvider>,
  )
}

describe('ConnectionProvider', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('marks the app connected and exposes counts once info resolves', async () => {
    mockedApi.info.mockResolvedValue({ connected: true, version: '1.15.16' })
    mockedApi.overview.mockResolvedValue({
      counts: { sessions: 3, beacons: 2, jobs: 1, builders: 0, socks: 0 },
    })

    const view = renderProvider()
    await waitFor(() => expect(screen.getByTestId('ready').textContent).toBe('true'))
    expect(screen.getByTestId('connected').textContent).toBe('true')
    expect(screen.getByTestId('version').textContent).toBe('1.15.16')
    expect(screen.getByTestId('count-sessions').textContent).toBe('3')
    view.unmount()
  })

  it('does not fetch overview when the server is unreachable', async () => {
    mockedApi.info.mockResolvedValue({ connected: false, version: '' })

    const view = renderProvider()
    await waitFor(() => expect(screen.getByTestId('ready').textContent).toBe('true'))
    expect(screen.getByTestId('connected').textContent).toBe('false')
    expect(mockedApi.overview).not.toHaveBeenCalled()
    view.unmount()
  })

  it('stays connected even when overview collection fails', async () => {
    mockedApi.info.mockResolvedValue({ connected: true, version: '1.15.16' })
    mockedApi.overview.mockRejectedValue(new Error('overview timed out'))

    const view = renderProvider()
    await waitFor(() => expect(screen.getByTestId('ready').textContent).toBe('true'))
    expect(screen.getByTestId('connected').textContent).toBe('true')
    expect(screen.getByTestId('count-sessions').textContent).toBe('0')
    view.unmount()
  })

  it('stays disconnected when info rejects', async () => {
    mockedApi.info.mockRejectedValue(new Error('connection refused'))

    const view = renderProvider()
    await waitFor(() => expect(screen.getByTestId('ready').textContent).toBe('true'))
    expect(screen.getByTestId('connected').textContent).toBe('false')
    expect(mockedApi.overview).not.toHaveBeenCalled()
    view.unmount()
  })
})
