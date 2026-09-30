import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import FilesTab from '../FilesTab'
import PostExTab from '../PostExTab'
import PortfwdTab from '../PortfwdTab'
import { ToastProvider } from '../../common/Toast'
import { api } from '../../../lib/api'

vi.mock('../../../lib/api', () => ({
  api: {
    fsList: vi.fn(),
    fsCat: vi.fn(),
    fsMkdir: vi.fn(),
    fsUpload: vi.fn(),
    fsRm: vi.fn(),
    fsDownload: vi.fn(),
    fsMv: vi.fn(),
    grep: vi.fn(),
    memfiles: vi.fn(),
    portfwdList: vi.fn(),
    portfwdStart: vi.fn(),
    portfwdStop: vi.fn(),
    rportfwd: vi.fn(),
    rportfwdStart: vi.fn(),
    rportfwdStop: vi.fn(),
    wasmExtensions: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

function renderWithProviders(node: ReactElement) {
  return render(<ToastProvider>{node}</ToastProvider>)
}

const DIR = {
  Path: '/etc',
  Files: [{ Name: 'nginx.conf', IsDir: false, Size: 512, Mode: '-rw-r--r--', ModTime: 1700000000 }],
}

const HIT = {
  SearchPath: '/etc',
  Results: [
    {
      Path: '/etc/nginx/nginx.conf',
      IsBinary: false,
      Matches: [{ LineNumber: 42, Line: 'proxy_pass http://127.0.0.1:8080;', LinesBefore: [], LinesAfter: [] }],
    },
  ],
}

describe('FilesTab content search', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedApi.fsList.mockResolvedValue(DIR as never)
  })

  it('keeps the search panel closed until it is asked for', async () => {
    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await screen.findByText('nginx.conf')

    expect(screen.queryByPlaceholderText('Pattern (regex)')).toBeNull()
    fireEvent.click(screen.getByText('Content search'))
    expect(await screen.findByPlaceholderText('Pattern (regex)')).toBeTruthy()
  })

  it('searches the directory the browser is sitting in', async () => {
    mockedApi.grep.mockResolvedValue(HIT as never)
    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await screen.findByText('nginx.conf')

    fireEvent.click(screen.getByText('Content search'))
    fireEvent.change(await screen.findByPlaceholderText('Pattern (regex)'), {
      target: { value: 'proxy_pass' },
    })
    fireEvent.click(screen.getByText('Search'))

    await waitFor(() =>
      expect(mockedApi.grep).toHaveBeenCalledWith('s1', 'proxy_pass', '/etc', true, 0, 0),
    )
  })

  it('renders the matching line with its line number', async () => {
    mockedApi.grep.mockResolvedValue(HIT as never)
    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await screen.findByText('nginx.conf')

    fireEvent.click(screen.getByText('Content search'))
    fireEvent.change(await screen.findByPlaceholderText('Pattern (regex)'), {
      target: { value: 'proxy_pass' },
    })
    fireEvent.click(screen.getByText('Search'))

    expect(await screen.findByText('/etc/nginx/nginx.conf')).toBeTruthy()
    expect(await screen.findByText('42:')).toBeTruthy()
    expect(await screen.findByText('proxy_pass http://127.0.0.1:8080;')).toBeTruthy()
  })

  it('says so when nothing matched, instead of showing an empty table', async () => {
    mockedApi.grep.mockResolvedValue({ SearchPath: '/etc', Results: [] } as never)
    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await screen.findByText('nginx.conf')

    fireEvent.click(screen.getByText('Content search'))
    fireEvent.change(await screen.findByPlaceholderText('Pattern (regex)'), {
      target: { value: 'nothinghere' },
    })
    fireEvent.click(screen.getByText('Search'))

    expect(await screen.findByText('No matches')).toBeTruthy()
  })

  it('refuses to search on an empty pattern', async () => {
    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await screen.findByText('nginx.conf')

    fireEvent.click(screen.getByText('Content search'))
    fireEvent.click(screen.getByText('Search'))

    await waitFor(() => expect(screen.getByText('Enter a search pattern')).toBeTruthy())
    expect(mockedApi.grep).not.toHaveBeenCalled()
  })
})

describe('PostExTab after both moves', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedApi.memfiles.mockResolvedValue({ Path: '', Files: [] } as never)
    mockedApi.wasmExtensions.mockResolvedValue({ extensions: [] } as never)
  })

  it('keeps only the panes that were not folded elsewhere', async () => {
    renderWithProviders(<PostExTab sessionId="s1" />)
    await screen.findByText('File attributes')

    expect(screen.queryByText('Content search')).toBeNull()
    expect(screen.queryByText('Reverse port forward')).toBeNull()
    // The remaining panes are untouched by the moves.
    expect(screen.getByText('Memfiles')).toBeTruthy()
    expect(screen.getByText('WASM extensions')).toBeTruthy()
  })

  it('never calls the reverse-forward API now that the pane is gone', async () => {
    renderWithProviders(<PostExTab sessionId="s1" />)
    await screen.findByText('File attributes')

    expect(mockedApi.rportfwd).not.toHaveBeenCalled()
  })
})

describe('PortfwdTab holding both directions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedApi.portfwdList.mockResolvedValue({ forwards: [] } as never)
    // The reverse table loads on mount, so every test in this block needs a stub
    // even when it is only interested in the local form.
    mockedApi.rportfwd.mockResolvedValue({ listeners: [] } as never)
  })

  it('shows a local form and a reverse form side by side in one tab', async () => {
    renderWithProviders(<PortfwdTab sessionId="s1" />)
    await screen.findByText('Port Forwarding')

    expect(screen.getByText('Reverse Port Forwarding')).toBeTruthy()
    // Each direction names its own roles, so the four shared values are not ambiguous.
    expect(screen.getByText('Target Port')).toBeTruthy()
    expect(screen.getByText('Local Host')).toBeTruthy()
    // And the local form is still there.
    expect(screen.getByText('Remote Host')).toBeTruthy()
  })

  it('loads reverse listeners when the tab mounts, not on a click', async () => {
    mockedApi.rportfwd.mockResolvedValue({ listeners: [] } as never)
    renderWithProviders(<PortfwdTab sessionId="s1" />)
    await screen.findByText('Reverse Port Forwarding')

    await waitFor(() => expect(mockedApi.rportfwd).toHaveBeenCalledWith('s1'))
    expect(await screen.findByText('No active reverse listeners')).toBeTruthy()
  })

  it('reports both live listeners in their own table', async () => {
    mockedApi.rportfwd.mockResolvedValue({
      listeners: [
        {
          ID: 7,
          BindAddress: '0.0.0.0',
          BindPort: 4444,
          ForwardAddress: '127.0.0.1',
          ForwardPort: 9001,
        },
      ],
    } as never)
    renderWithProviders(<PortfwdTab sessionId="s1" />)

    expect(await screen.findByText('0.0.0.0:4444')).toBeTruthy()
    expect(screen.getByText('127.0.0.1:9001')).toBeTruthy()
  })

  it('refuses to start a reverse listener without both ports', async () => {
    mockedApi.rportfwd.mockResolvedValue({ listeners: [] } as never)
    renderWithProviders(<PortfwdTab sessionId="s1" />)
    await screen.findByText('Reverse Port Forwarding')

    fireEvent.click(screen.getByText('Start Listener'))

    await waitFor(() =>
      expect(screen.getByText('Enter both the target port and the local port')).toBeTruthy(),
    )
    expect(mockedApi.rportfwdStart).not.toHaveBeenCalled()
  })
})
