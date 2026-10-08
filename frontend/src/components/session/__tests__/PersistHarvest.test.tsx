import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import PersistenceTab from '../PersistenceTab'
import HarvestTab from '../HarvestTab'
import { ToastProvider } from '../../common/Toast'
import { api } from '../../../lib/api'
import type { PersistenceModule } from '../../../lib/types'

vi.mock('../../../lib/api', () => ({
  api: {
    persistenceModules: vi.fn(),
    persistenceList: vi.fn(),
    persistenceInstall: vi.fn(),
    persistenceRemove: vi.fn(),
    mimikatz: vi.fn(),
    mimikatzParse: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

const MODULE: PersistenceModule = {
  id: 'registry-run',
  name: 'Registry Run key',
  technique: 'T1547.001',
  platforms: ['windows'],
  requiresAdmin: false,
  description: 'Runs the payload at user logon.',
}

/** Both tabs read toasts, so they need the provider the app wraps pages in. */
function renderWithProviders(node: ReactElement) {
  return render(<ToastProvider>{node}</ToastProvider>)
}

const PAYLOAD = 'C:\\Windows\\Temp\\agent.exe'

describe('PersistenceTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedApi.persistenceModules.mockResolvedValue({ modules: [MODULE] })
    mockedApi.persistenceList.mockResolvedValue({
      platform: 'windows',
      items: [
        {
          module: 'registry-run',
          name: 'registry-run',
          location: 'HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run',
          installed: true,
          detail: 'present',
          removable: true,
        },
      ],
    })
  })

  // Opening the tab must not touch the target. The inventory is answered by
  // running a query per module on the host -- roughly eighteen processes -- so
  // collecting it on mount would put that burst in the endpoint's process
  // telemetry every time an operator so much as looked at the page.
  it('loads only the catalog on mount, and leaves the target alone', async () => {
    renderWithProviders(<PersistenceTab sessionId="s1" os="windows" />)
    await waitFor(() => expect(screen.getByText('Registry Run key')).toBeInTheDocument())

    expect(mockedApi.persistenceModules).toHaveBeenCalled()
    expect(mockedApi.persistenceList).not.toHaveBeenCalled()
    expect(screen.getByText(/Nothing probed yet/)).toBeInTheDocument()
  })

  it('collects the inventory only when Rescan is clicked', async () => {
    renderWithProviders(<PersistenceTab sessionId="s1" os="windows" />)
    await waitFor(() => expect(screen.getByText('Registry Run key')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Rescan'))

    // No name typed, so the inventory runs anonymously: undefined, not ''.
    await waitFor(() => expect(mockedApi.persistenceList).toHaveBeenCalledWith('s1', undefined))
    expect(screen.getByText('1 of 1 present')).toBeInTheDocument()
    expect(screen.getAllByText('T1547.001').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Present').length).toBeGreaterThan(0)
  })

  // Name-scoped mechanisms come back marked unknown, because the anonymous
  // inventory pass has nothing to look them up by. The row must say so rather
  // than claiming the host is clean right after an install.
  it('shows an unknown row as needing a name, then resolves it once one is given', async () => {
    mockedApi.persistenceList.mockResolvedValue({
      platform: 'windows',
      items: [
        {
          module: 'win-watchdog',
          name: 'Keep-alive task',
          location: 'Task Scheduler\\c2Watchdog (every 5m)',
          installed: false,
          detail: 'needs an artifact name to check',
          removable: false,
          unknown: true,
        },
      ],
    })

    renderWithProviders(<PersistenceTab sessionId="s1" os="windows" />)
    await waitFor(() => expect(screen.getByText('Registry Run key')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Rescan'))
    await waitFor(() => expect(screen.getByText('Needs a name')).toBeInTheDocument())
    // The distinguishing claim: it must not read as absent.
    expect(screen.queryByText('Absent')).not.toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText('Check a name (optional)'), {
      target: { value: 'c2Watchdog' },
    })

    // Typing does not re-scan on its own. It used to, and that meant one query
    // per keystroke against the target; the name is an input to the sweep, not a
    // trigger for it. Rescan is the trigger.
    expect(mockedApi.persistenceList).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByText('Rescan'))

    await waitFor(() =>
      expect(mockedApi.persistenceList).toHaveBeenLastCalledWith('s1', 'c2Watchdog'),
    )
  })

  it('reveals the install form with the module defaults when a card is clicked', async () => {
    renderWithProviders(<PersistenceTab sessionId="s1" os="windows" />)
    await waitFor(() => expect(screen.getByText('Registry Run key')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Registry Run key'))

    const payload = (await screen.findByPlaceholderText(PAYLOAD)) as HTMLInputElement
    expect(payload.value).toBe(PAYLOAD)
    expect((screen.getByDisplayValue('registry-run') as HTMLInputElement).value).toBe('registry-run')
    expect(screen.getByText('Install')).toBeInTheDocument()
  })

  // The payload value is submitted to the target verbatim, so a Linux session
  // must not be seeded with a Windows path: a C:\ default there would install
  // a cron entry that silently does nothing, and the operator would read the
  // empty result as the technique failing rather than the path being wrong.
  it('seeds the payload with a POSIX path for a Linux session', async () => {
    renderWithProviders(<PersistenceTab sessionId="s1" os="linux" />)
    await waitFor(() => expect(screen.getByText('Registry Run key')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Registry Run key'))

    const payload = (await screen.findByPlaceholderText('/tmp/agent')) as HTMLInputElement
    expect(payload.value).toBe('/tmp/agent')
  })

  it('installs the selected module and refreshes the list', async () => {
    mockedApi.persistenceInstall.mockResolvedValue({
      ok: true,
      message: 'Installed',
      module: 'registry-run',
      location: 'HKCU\\...\\Run',
    })
    renderWithProviders(<PersistenceTab sessionId="s1" os="windows" />)
    await waitFor(() => expect(screen.getByText('Registry Run key')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Rescan'))
    await waitFor(() => expect(screen.getByText('1 of 1 present')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Registry Run key'))
    await screen.findByPlaceholderText(PAYLOAD)

    fireEvent.click(screen.getByText('Install'))

    await waitFor(() =>
      expect(mockedApi.persistenceInstall).toHaveBeenCalledWith('s1', 'registry-run', PAYLOAD, 'registry-run'),
    )
    // Once from mount, once after the install succeeded.
    await waitFor(() => expect(mockedApi.persistenceList).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByText('Installed')).toBeInTheDocument())
  })

  it('removes a removable item from the present list', async () => {
    mockedApi.persistenceRemove.mockResolvedValue({
      ok: true,
      message: 'Removed',
      module: 'registry-run',
      location: '',
    })
    renderWithProviders(<PersistenceTab sessionId="s1" os="windows" />)
    await waitFor(() => expect(screen.getByText('Registry Run key')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Rescan'))
    await waitFor(() => expect(screen.getByText('1 of 1 present')).toBeInTheDocument())

    fireEvent.click(screen.getAllByText('Remove')[0])

    await waitFor(() =>
      expect(mockedApi.persistenceRemove).toHaveBeenCalledWith('s1', 'registry-run', 'registry-run'),
    )
  })
})

describe('HarvestTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('offers the curated command list with logonpasswords default', async () => {
    renderWithProviders(<HarvestTab sessionId="s1" />)

    const select = (await screen.findByLabelText('Command')) as HTMLSelectElement
    expect(select.value).toBe('sekurlsa::logonpasswords')
    expect(screen.getByRole('option', { name: 'lsadump::lsa /patch' })).toBeInTheDocument()
    // No path field: the console ships the binary and writes it to the target's
    // own temp directory.
    expect(screen.queryByLabelText('Target path')).not.toBeInTheDocument()
    expect((screen.getByLabelText('Auto-add to vault') as HTMLInputElement).checked).toBe(true)
  })

  it('runs the command and renders the parsed credentials with a count', async () => {
    mockedApi.mimikatz.mockResolvedValue({
      ok: true,
      command: 'sekurlsa::logonpasswords',
      raw: 'raw output',
      exitCode: 0,
      parsed: [{ username: 'admin', domain: 'CORP', secret: 'hunter2', kind: 'plaintext', source: 'sekurlsa' }],
      added: 1,
      message: 'Parsed 1 credential',
    })
    renderWithProviders(<HarvestTab sessionId="s1" />)
    await screen.findByLabelText('Command')

    fireEvent.click(screen.getByText('Run'))

    await waitFor(() =>
      expect(mockedApi.mimikatz).toHaveBeenCalledWith(
        's1',
        'sekurlsa::logonpasswords',
        true,
        // Escalation defaults on: without it the LSA-reading modules fail with
        // an access-denied that looks like a broken tool.
        true,
        undefined,
        // The execution mode and injection host are sent on every run: auto is
        // the default, and an empty host means "use the console default".
        'auto',
        undefined,
      ),
    )
    await waitFor(() => expect(screen.getByText('admin')).toBeInTheDocument())
    expect(screen.getByText('CORP')).toBeInTheDocument()
    expect(screen.getByText('hunter2')).toBeInTheDocument()
    expect(screen.getByText('Parsed credentials (1)')).toBeInTheDocument()
    expect(screen.getByText('Raw output')).toBeInTheDocument()
  })

  it('hides the add-to-vault button when the run already stored its findings', async () => {
    mockedApi.mimikatz.mockResolvedValue({
      ok: true,
      command: 'sekurlsa::msv',
      raw: 'x',
      exitCode: 0,
      parsed: [{ username: 'u', domain: 'd', secret: 's', kind: 'ntlm', source: 'sekurlsa' }],
      added: 1,
      message: '',
    })
    renderWithProviders(<HarvestTab sessionId="s1" />)
    await screen.findByLabelText('Command')
    fireEvent.click(screen.getByText('Run'))

    await waitFor(() => expect(screen.getByText('Parsed credentials (1)')).toBeInTheDocument())
    expect(screen.queryByText(/Add 1 to vault/)).not.toBeInTheDocument()
  })

  it('offers add-to-vault when the operator turned auto-add off', async () => {
    mockedApi.mimikatz.mockResolvedValue({
      ok: true,
      command: 'sekurlsa::msv',
      raw: 'x',
      exitCode: 0,
      parsed: [{ username: 'u', domain: 'd', secret: 's', kind: 'ntlm', source: 'sekurlsa' }],
      added: 0,
      message: '',
    })
    mockedApi.mimikatzParse.mockResolvedValue({
      ok: true,
      command: 'parse',
      raw: 'x',
      exitCode: 0,
      parsed: [{ username: 'u', domain: 'd', secret: 's', kind: 'ntlm', source: 'sekurlsa' }],
      added: 1,
      message: '',
    })
    renderWithProviders(<HarvestTab sessionId="s1" />)
    await screen.findByLabelText('Command')
    fireEvent.click(screen.getByLabelText('Auto-add to vault'))
    fireEvent.click(screen.getByText('Run'))

    const button = await screen.findByText('Add 1 to vault')
    fireEvent.click(button)

    await waitFor(() => expect(mockedApi.mimikatzParse).toHaveBeenCalledWith('x', true))
  })

  it('parses pasted output', async () => {
    mockedApi.mimikatzParse.mockResolvedValue({
      ok: true,
      command: 'parse',
      raw: 'pasted',
      exitCode: 0,
      parsed: [{ username: 'svc', domain: '', secret: 'abc', kind: 'sha1', source: 'paste' }],
      added: 0,
      message: '',
    })
    renderWithProviders(<HarvestTab sessionId="s1" />)

    const box = (await screen.findByPlaceholderText(/Paste sekurlsa/)) as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'some captured output' } })
    fireEvent.click(screen.getByText('Parse'))

    await waitFor(() => expect(mockedApi.mimikatzParse).toHaveBeenCalledWith('some captured output', true))
    await waitFor(() => expect(screen.getByText('svc')).toBeInTheDocument())
  })

  it('refuses to parse an empty paste area', async () => {
    renderWithProviders(<HarvestTab sessionId="s1" />)
    await screen.findByText('Parse')

    fireEvent.click(screen.getByText('Parse'))

    await waitFor(() => expect(screen.getByText('Paste some output to parse')).toBeInTheDocument())
    expect(mockedApi.mimikatzParse).not.toHaveBeenCalled()
  })
})
