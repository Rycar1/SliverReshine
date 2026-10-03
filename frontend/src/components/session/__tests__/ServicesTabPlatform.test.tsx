import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import ServicesTab from '../ServicesTab'
import { api } from '../../../lib/api'

vi.mock('../../../lib/api', () => ({
  api: {
    listExtensions: vi.fn(),
    registerExtension: vi.fn(),
    callExtension: vi.fn(),
    startService: vi.fn(),
    stopService: vi.fn(),
    removeService: vi.fn(),
    runSSHCommand: vi.fn(),
    creds: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

beforeEach(() => {
  vi.clearAllMocks()
  mockedApi.creds.mockResolvedValue({ credentials: [] })
  mockedApi.listExtensions.mockResolvedValue({ names: [] })
})

/**
 * Sliver's Linux implant has no service or extension handler, so the server
 * answers "unknown message type" (a 501). The tab used to call listExtensions on
 * mount for every platform, which painted that error over a Linux session the
 * moment the page opened. These tests pin the platform gate on both the request
 * and the UI.
 */
describe('ServicesTab platform gating', () => {
  it('does not ask a Linux session for extensions and says why the cards are unavailable', async () => {
    render(<ServicesTab sessionId="s1" os="linux" />)

    await waitFor(() =>
      expect(screen.getByText(/Windows services require a Windows target/)).toBeInTheDocument(),
    )
    expect(screen.getByText(/extensions require a Windows or macOS target/)).toBeInTheDocument()
    // The point of the fix: no RPC that the Linux implant cannot answer.
    expect(mockedApi.listExtensions).not.toHaveBeenCalled()
    // The forms are gone, not merely failing on submit.
    expect(screen.queryByText('Service name')).not.toBeInTheDocument()
    expect(screen.queryByText('Archive file')).not.toBeInTheDocument()
    // SSH is platform-neutral and must survive the gating.
    expect(screen.getByText('SSH Command')).toBeInTheDocument()
  })

  it('offers both cards on a Windows session', async () => {
    render(<ServicesTab sessionId="s2" os="windows" />)

    await waitFor(() => expect(mockedApi.listExtensions).toHaveBeenCalledWith('s2'))
    expect(screen.getByText('Service name')).toBeInTheDocument()
    expect(screen.getByText('Archive file')).toBeInTheDocument()
    expect(screen.queryByText(/Not available on this session/)).not.toBeInTheDocument()
  })

  it('offers extensions but not services on a macOS session', async () => {
    render(<ServicesTab sessionId="s3" os="darwin" />)

    await waitFor(() => expect(mockedApi.listExtensions).toHaveBeenCalledWith('s3'))
    expect(screen.getByText('Archive file')).toBeInTheDocument()
    expect(screen.queryByText('Service name')).not.toBeInTheDocument()
    expect(screen.getByText(/Windows services require a Windows target/)).toBeInTheDocument()
  })
})
