import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import type { ReactElement } from 'react'
import ListenersPage from '../ListenersPage'
import { ToastProvider } from '../../components/common/Toast'
import { api } from '../../lib/api'
import type { Job } from '../../lib/types'

vi.mock('../../lib/api', () => ({
  api: {
    jobs: vi.fn(),
    bindListeners: vi.fn(),
    startListener: vi.fn(),
    stopListener: vi.fn(),
    dialBind: vi.fn(),
    oneLinerTargets: vi.fn(),
    oneLiner: vi.fn(),
    oneLinerAll: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

/** Both the page and the panel above it read toasts. */
function renderWithProviders(node: ReactElement) {
  return render(<ToastProvider>{node}</ToastProvider>)
}

function job(over: Partial<Job>): Job {
  return { ID: 1, Name: 'mtls', Protocol: 'mtls', Port: 8888, Domains: [], JobControl: '', ...over }
}

/**
 * Only the HTTP family can host a stage, so the row's "Staging command" button
 * is disabled for everything else. It used to be disabled with the reason in a
 * tooltip only, which reads as a broken button: the operator clicks, nothing
 * happens, and there is no visible explanation. These tests pin the gate and the
 * visible reason, and pin that the gate follows the server's CanStage rather
 * than a name comparison re-derived in the browser.
 */
describe('ListenersPage staging button', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedApi.bindListeners.mockResolvedValue({ listeners: [] })
    mockedApi.oneLinerTargets.mockResolvedValue({ targets: [] })
  })

  function rowFor(text: string) {
    // The listener name and its protocol badge are the same string for mTLS,
    // DNS and the like, so a single-text query matches twice.
    const row = screen.getAllByRole('row').find((r) => within(r).queryAllByText(text).length > 0)
    if (!row) throw new Error('no table row containing ' + text)
    return row
  }

  it('enables staging for HTTP and explains the refusal inline for everything else', async () => {
    mockedApi.jobs.mockResolvedValue({
      jobs: [
        job({ ID: 1, Name: 'http', Protocol: 'http', Port: 8080, CanStage: true }),
        job({ ID: 2, Name: 'mtls', Protocol: 'mtls', Port: 8888, CanStage: false }),
        job({ ID: 3, Name: 'dns', Protocol: 'dns', Port: 53, CanStage: false }),
      ],
    })

    renderWithProviders(<ListenersPage />)

    await waitFor(() => expect(screen.getAllByText('mtls').length).toBeGreaterThan(0))

    const httpBtn = within(rowFor('http')).getByRole('button', { name: 'Staging command' })
    expect(httpBtn).toBeEnabled()
    expect(within(rowFor('http')).queryByText('HTTP(S) listener only')).not.toBeInTheDocument()

    const mtlsRow = rowFor('mtls')
    expect(within(mtlsRow).getByRole('button', { name: 'Staging command' })).toBeDisabled()
    // The visible reason is the fix: no hover required.
    expect(within(mtlsRow).getByText('HTTP(S) listener only')).toBeInTheDocument()

    expect(within(rowFor('dns')).getByRole('button', { name: 'Staging command' })).toBeDisabled()
  })

  it('follows the server CanStage flag even when the listener is named http', async () => {
    // The server is the authority on what it will accept. If it says no, the
    // button must be off, name notwithstanding.
    mockedApi.jobs.mockResolvedValue({
      jobs: [job({ ID: 9, Name: 'http', Protocol: 'http', Port: 80, CanStage: false })],
    })

    renderWithProviders(<ListenersPage />)

    await waitFor(() => expect(screen.getAllByText('http').length).toBeGreaterThan(0))
    expect(within(rowFor('http')).getByRole('button', { name: 'Staging command' })).toBeDisabled()
    expect(within(rowFor('http')).getByText('HTTP(S) listener only')).toBeInTheDocument()
  })

  it('disables the context-menu staging item with the same reason', async () => {
    mockedApi.jobs.mockResolvedValue({
      jobs: [job({ ID: 2, Name: 'mtls', Protocol: 'mtls', Port: 8888, CanStage: false })],
    })

    renderWithProviders(<ListenersPage />)
    await waitFor(() => expect(screen.getAllByText('mtls').length).toBeGreaterThan(0))

    fireEvent.contextMenu(rowFor('mtls'))

    const item = await screen.findByRole('menuitem', { name: /Staging command/ })
    expect(item).toBeDisabled()
    expect(item).toHaveTextContent('HTTP(S) listener only')
  })
})
