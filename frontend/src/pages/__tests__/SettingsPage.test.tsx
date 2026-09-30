import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import SettingsPage from '../../pages/SettingsPage'
import { ConnectionProvider } from '../../lib/connection'
import { api } from '../../lib/api'

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

// jsdom's FileReader is asynchronous and awkward to drive in tests; use a
// deterministic fake that resolves the file text on readAsText.
class FakeFileReader {
  result = ''
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  readAsText(blob: Blob) {
    blob.text().then((text) => {
      this.result = text
      this.onload?.()
    })
  }
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ConnectionProvider>
        <SettingsPage />
      </ConnectionProvider>
    </MemoryRouter>,
  )
}

function mockConnected(connected: boolean) {
  mockedApi.info.mockResolvedValue(connected ? { connected: true, version: '1.15.16' } : { connected: false, version: '' })
  mockedApi.overview.mockResolvedValue({
    counts: { sessions: 0, beacons: 0, jobs: 0, builders: 0, socks: 0 },
  })
}

const sampleConfig = {
  operator: 'local',
  lhost: '127.0.0.1',
  lport: 31337,
  ca_certificate: 'ca',
  certificate: 'cert',
  private_key: 'key',
}
const sampleConfigText = JSON.stringify(sampleConfig)

async function loadConfigFile() {
  const input = screen.getByLabelText('Select Config File') as HTMLInputElement
  const file = new File([sampleConfigText], 'local.json', { type: 'application/json' })
  Object.defineProperty(input, 'files', { value: [file], configurable: true })
  fireEvent.change(input)
  await waitFor(() => {
    expect((screen.getByDisplayValue('127.0.0.1') as HTMLInputElement).value).toBe('127.0.0.1')
  })
}

describe('SettingsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal('FileReader', FakeFileReader)
    mockedApi.listProfiles.mockResolvedValue({ profiles: ['local', 'lab'] })
    mockConnected(false)
  })

  it('renders saved profiles', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('local')).toBeInTheDocument())
    expect(screen.getByText('lab')).toBeInTheDocument()
    view.unmount()
  })

  it('replaced the manual input form with a config-file loader', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Select Config File')).toBeInTheDocument())
    expect(screen.queryByText('Manual Connection')).not.toBeInTheDocument()
    view.unmount()
  })

  it('connects from a loaded config file', async () => {
    mockedApi.connect.mockResolvedValue({ success: true })
    const view = renderPage()
    await loadConfigFile()
    fireEvent.click(screen.getByText('Connect'))
    await waitFor(() => expect(screen.getByText('Connected')).toBeInTheDocument())
    expect(mockedApi.connect).toHaveBeenCalledWith({ content: sampleConfigText })
    view.unmount()
  })

  it('shows an error banner when connect from file fails', async () => {
    mockedApi.connect.mockRejectedValue(new Error('boom'))
    const view = renderPage()
    await loadConfigFile()
    fireEvent.click(screen.getByText('Connect'))
    await waitFor(() => expect(screen.getByText(/boom/)).toBeInTheDocument())
    view.unmount()
  })

  it('shows an error banner when the config file is invalid', async () => {
    const view = renderPage()
    const input = screen.getByLabelText('Select Config File') as HTMLInputElement
    const file = new File(['not json'], 'bad.json', { type: 'application/json' })
    Object.defineProperty(input, 'files', { value: [file], configurable: true })
    fireEvent.change(input)
    await waitFor(() => expect(screen.getByText(/Invalid config file/)).toBeInTheDocument())
    view.unmount()
  })

  it('shows an error banner when profile loading fails', async () => {
    mockedApi.listProfiles.mockRejectedValue(new Error('profile service down'))
    const view = renderPage()
    await waitFor(() => expect(screen.getByText(/profile service down/)).toBeInTheDocument())
    view.unmount()
  })

  it('uses a profile and confirms', async () => {
    mockedApi.useProfile.mockResolvedValue({ success: true })
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('lab')).toBeInTheDocument())
    fireEvent.click(screen.getByText('lab'))
    await waitFor(() => expect(screen.getByText('Using profile lab')).toBeInTheDocument())
    expect(mockedApi.useProfile).toHaveBeenCalledWith('lab')
    view.unmount()
  })

  it('shows the disconnect button when connected', async () => {
    mockConnected(true)
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Disconnect')).toBeInTheDocument())
    view.unmount()
  })

  it('disconnects and confirms', async () => {
    mockConnected(true)
    mockedApi.disconnect.mockResolvedValue({ success: true })
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Disconnect')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Disconnect'))
    await waitFor(() => expect(screen.getByText('Disconnected')).toBeInTheDocument())
    expect(mockedApi.disconnect).toHaveBeenCalled()
    view.unmount()
  })
})
