import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import ProcessesTab from '../ProcessesTab'
import { api } from '../../../lib/api'

vi.mock('../../../lib/api', () => ({
  api: {
    ps: vi.fn(),
    killProcess: vi.fn(),
    migrate: vi.fn(),
    processDump: vi.fn(),
    avScan: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

const PROCESSES = [
  { PID: 100, PPID: 1, Executable: 'nginx', Owner: 'root', CmdLine: ['nginx', '-g', 'daemon off;'] },
]

beforeEach(() => {
  vi.clearAllMocks()
  mockedApi.ps.mockResolvedValue({ processes: PROCESSES } as never)
})

/**
 * Sliver's Linux and macOS implants implement neither Migrate nor ProcessDump,
 * so both RPCs come back as "unknown message type". The buttons used to be
 * offered on every session, which turned a platform fact into what looked like a
 * console bug. They are Windows-only now, and the tab says so on the others.
 */
describe('ProcessesTab platform gating', () => {
  it('hides migrate and dump on a Linux session and explains why', async () => {
    render(<ProcessesTab sessionId="s1" os="linux" />)

    expect(await screen.findByText('nginx')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Migrate' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Dump' })).not.toBeInTheDocument()
    expect(screen.getByText(/migrate and dump require a Windows target/)).toBeInTheDocument()
    // Kill is platform-neutral and must survive the gating.
    expect(screen.getByRole('button', { name: 'Kill' })).toBeInTheDocument()
  })

  it('offers migrate and dump on a Windows session', async () => {
    render(<ProcessesTab sessionId="s2" os="windows" />)

    expect(await screen.findByText('nginx')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Migrate' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Dump' })).toBeInTheDocument()
    expect(
      screen.queryByText(/migrate and dump require a Windows target/),
    ).not.toBeInTheDocument()
  })
})
