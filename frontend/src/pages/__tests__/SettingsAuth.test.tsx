import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import SettingsPage from '../../pages/SettingsPage'
import { ConnectionProvider } from '../../lib/connection'
import { ToastProvider } from '../../components/common/Toast'
import { api } from '../../lib/api'

vi.mock('../../lib/api', () => ({
  api: {
    info: vi.fn(),
    overview: vi.fn(),
    listProfiles: vi.fn(),
    connect: vi.fn(),
    disconnect: vi.fn(),
    useProfile: vi.fn(),
    authGet: vi.fn(),
    authPut: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

function renderPage() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <ConnectionProvider>
          <SettingsPage />
        </ConnectionProvider>
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('SettingsPage console authentication panel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedApi.info.mockResolvedValue({ connected: false, version: '' })
    mockedApi.overview.mockResolvedValue({
      counts: { sessions: 0, beacons: 0, jobs: 0, builders: 0, socks: 0 },
    })
    mockedApi.listProfiles.mockResolvedValue({ profiles: [] })
    mockedApi.authGet.mockResolvedValue({ username: 'operator', enabled: true, source: 'console-auth' })
  })

  it('shows the current username from the API, read-only', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByLabelText('Current username')).toBeInTheDocument())
    const input = screen.getByLabelText('Current username') as HTMLInputElement
    expect(input.value).toBe('operator')
    expect(input).toHaveAttribute('readonly')
    expect(mockedApi.authGet).toHaveBeenCalled()
    view.unmount()
  })

  it('refuses a password shorter than eight characters without calling the API', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByLabelText('New password')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'short' } })
    fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: 'short' } })
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'old-secret' } })
    fireEvent.click(screen.getByText('Save'))

    await waitFor(() =>
      expect(screen.getByText('The password must be at least 8 characters')).toBeInTheDocument(),
    )
    expect(mockedApi.authPut).not.toHaveBeenCalled()
    view.unmount()
  })

  it('refuses a mismatched confirmation without calling the API', async () => {
    const view = renderPage()
    await waitFor(() => expect(screen.getByLabelText('New password')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'longenough1' } })
    fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: 'longenough2' } })
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'old-secret' } })
    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(screen.getByText('The two passwords do not match')).toBeInTheDocument())
    expect(mockedApi.authPut).not.toHaveBeenCalled()
    view.unmount()
  })

  it('saves a valid change and tells the operator it takes effect immediately', async () => {
    mockedApi.authPut.mockResolvedValue({ ok: true, message: 'updated' })
    const view = renderPage()
    await waitFor(() => expect(screen.getByLabelText('New password')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('New username'), { target: { value: 'newop' } })
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'longenough1' } })
    fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: 'longenough1' } })
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'old-secret' } })
    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(mockedApi.authPut).toHaveBeenCalledWith('newop', 'longenough1', 'old-secret'))
    await waitFor(() =>
      expect(screen.getByText(/browsers will re-prompt for credentials/i)).toBeInTheDocument(),
    )
    view.unmount()
  })

  it('surfaces a server-side rejection as an inline error', async () => {
    mockedApi.authPut.mockRejectedValue(new Error('current password is incorrect'))
    const view = renderPage()
    await waitFor(() => expect(screen.getByLabelText('New password')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'longenough1' } })
    fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: 'longenough1' } })
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'wrong' } })
    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(screen.getByText(/current password is incorrect/)).toBeInTheDocument())
    view.unmount()
  })
})
