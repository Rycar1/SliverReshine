import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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

// A profile as it comes off disk: the certificate AND the mTLS private key.
const sampleConfig = {
  operator: 'local',
  lhost: '127.0.0.1',
  lport: 31337,
  ca_certificate: 'ca',
  certificate: 'cert',
  private_key: 'PRIVATE-KEY-MATERIAL',
}
const sampleConfigText = JSON.stringify(sampleConfig)

// Storage is the thing under test, so read it through the real API rather than
// asserting on the component's internals.
function storedValues(): string[] {
  const out: string[] = []
  for (let i = 0; i < sessionStorage.length; i++) {
    const k = sessionStorage.key(i)
    if (k !== null) out.push(`${k}=${sessionStorage.getItem(k) ?? ''}`)
  }
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i)
    if (k !== null) out.push(`${k}=${localStorage.getItem(k) ?? ''}`)
  }
  return out
}

function anyStorageContains(needle: string): boolean {
  return storedValues().some((entry) => entry.includes(needle))
}

async function loadConfigFile() {
  const input = screen.getByLabelText('Select Config File') as HTMLInputElement
  const file = new File([sampleConfigText], 'local.json', { type: 'application/json' })
  Object.defineProperty(input, 'files', { value: [file], configurable: true })
  fireEvent.change(input)
  await waitFor(() => {
    expect((screen.getByDisplayValue('127.0.0.1') as HTMLInputElement).value).toBe('127.0.0.1')
  })
}

describe('SettingsPage private key storage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal('FileReader', FakeFileReader)
    sessionStorage.clear()
    localStorage.clear()
    mockedApi.listProfiles.mockResolvedValue({ profiles: ['local', 'lab'] })
    mockedApi.info.mockResolvedValue({ connected: false, version: '' })
    mockedApi.overview.mockResolvedValue({
      counts: { sessions: 0, beacons: 0, jobs: 0, builders: 0, socks: 0 },
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('keeps the private key out of Web Storage after loading a config file', async () => {
    const view = renderPage()
    await loadConfigFile()

    // The file parsed and the summary rendered, so the key is definitely in
    // memory -- it just must not be anywhere a script can read it back.
    expect((screen.getByDisplayValue('local') as HTMLInputElement).value).toBe('local')
    expect(anyStorageContains('PRIVATE-KEY-MATERIAL')).toBe(false)
    expect(anyStorageContains('private_key')).toBe(false)
    expect(anyStorageContains(sampleConfigText)).toBe(false)

    view.unmount()
  })

  it('writes no c2tool.config entry at all', async () => {
    const view = renderPage()
    await loadConfigFile()

    expect(sessionStorage.getItem('c2tool.config')).toBeNull()
    expect(localStorage.getItem('c2tool.config')).toBeNull()

    view.unmount()
  })

  it('still connects from the in-memory config', async () => {
    mockedApi.connect.mockResolvedValue({ success: true })
    const view = renderPage()
    await loadConfigFile()

    fireEvent.click(screen.getByText('Connect'))
    await waitFor(() => expect(screen.getByText('Connected')).toBeInTheDocument())
    expect(mockedApi.connect).toHaveBeenCalledWith({ content: sampleConfigText })

    view.unmount()
  })

  it('does not restore a previously stored config on mount', async () => {
    // Seed the key the old implementation used. A hardened page must ignore it
    // rather than adopting it, or an upgrade would inherit the old exposure.
    sessionStorage.setItem('c2tool.config', sampleConfigText)

    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Select Config File')).toBeInTheDocument())

    expect(screen.queryByDisplayValue('local')).not.toBeInTheDocument()
    const connect = screen.getByRole('button', { name: 'Connect' }) as HTMLButtonElement
    expect(connect.disabled).toBe(true)

    view.unmount()
  })

  it('tells the operator the key does not survive a reload', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('Select Config File')).toBeInTheDocument())

    expect(screen.getByText(/held in memory only/i)).toBeInTheDocument()

    view.unmount()
  })

  it('clears the in-memory key on disconnect', async () => {
    mockedApi.info.mockResolvedValue({ connected: true, version: '1.15.16' })
    mockedApi.disconnect.mockResolvedValue({ success: true })
    const view = renderPage()
    await loadConfigFile()

    await waitFor(() => expect(screen.getByText('Disconnect')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Disconnect'))

    await waitFor(() => expect(screen.getByText('Disconnected')).toBeInTheDocument())
    // The summary is gone and nothing reached storage on the way out.
    expect(screen.queryByDisplayValue('local')).not.toBeInTheDocument()
    expect(anyStorageContains('PRIVATE-KEY-MATERIAL')).toBe(false)

    view.unmount()
  })

  it('still persists the active profile name, which is not a secret', async () => {
    mockedApi.useProfile.mockResolvedValue({ success: true })
    const view = renderPage()
    await waitFor(() => expect(screen.getByText('lab')).toBeInTheDocument())

    fireEvent.click(screen.getByText('lab'))
    await waitFor(() => expect(localStorage.getItem('c2tool.activeProfile')).toBe('lab'))

    view.unmount()
  })
})
