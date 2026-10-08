import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import FilesTab from '../FilesTab'
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
  },
}))

const mockedApi = vi.mocked(api)

function renderWithProviders(node: ReactElement) {
  return render(<ToastProvider>{node}</ToastProvider>)
}

function listing(name: string, size: number) {
  return {
    Path: '/var/log',
    Files: [{ Name: name, IsDir: false, Size: size, Mode: '-rw-r--r--', ModTime: 1700000000 }],
  }
}

describe('FilesTab viewer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('refuses a file over the viewer limit without asking the target for it', async () => {
    mockedApi.fsList.mockResolvedValue(listing('huge.log', 6 * 1024 * 1024) as never)

    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await userEvent.click(await screen.findByText('huge.log'))

    expect(await screen.findByText(/File is too large/)).toBeInTheDocument()
    expect(mockedApi.fsCat).not.toHaveBeenCalled()
  })

  it('opens a small text file in the viewer', async () => {
    mockedApi.fsList.mockResolvedValue(listing('app.log', 128) as never)
    mockedApi.fsCat.mockResolvedValue({
      Name: '/var/log/app.log',
      Data: btoa('hello from the target'),
    } as never)

    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await userEvent.click(await screen.findByText('app.log'))

    await waitFor(() => expect(mockedApi.fsCat).toHaveBeenCalledTimes(1))
    expect(await screen.findByText('hello from the target')).toBeInTheDocument()
  })

  it('says a binary file cannot be previewed instead of showing its bytes', async () => {
    mockedApi.fsList.mockResolvedValue(listing('svc', 9) as never)
    mockedApi.fsCat.mockResolvedValue({ Name: '/var/log/svc', Binary: true, Size: 9 } as never)

    renderWithProviders(<FilesTab sessionId="s1" os="linux" />)
    await userEvent.click(await screen.findByText('svc'))

    expect(await screen.findByText(/Binary files cannot be previewed/)).toBeInTheDocument()
  })
})
