import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import WebDeliveryPage from '../WebDeliveryPage'

const { mockedApi, mockToast } = vi.hoisted(() => ({
  mockedApi: {
    implantProfiles: vi.fn(),
    webDeliveryFormats: vi.fn(),
    webDelivery: vi.fn(),
    websites: vi.fn(),
  },
  mockToast: { push: vi.fn() },
}))

vi.mock('../../lib/api', () => ({ api: mockedApi }))
vi.mock('../../components/common/Toast', () => ({ useToast: () => mockToast }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    // Echo the key with its interpolations so assertions can read them without
    // depending on the wording of either locale.
    t: (k: string, o?: Record<string, unknown>) =>
      o ? `${k}:${JSON.stringify(o)}` : k,
  }),
}))

const PROFILE = { Name: 'win-shellcode', OS: 'windows', Arch: 'amd64' }
const FORMATS = [
  { id: 'psh', platform: 'windows', label: 'PowerShell' },
  { id: 'curl', platform: 'linux', label: 'curl / wget' },
]

/**
 * Fills the host and submits.
 *
 * The build button stays disabled until the profile list has loaded, so a bare
 * click can land before the component is ready and silently do nothing. Waiting
 * for the button is what makes these tests deterministic.
 */
async function submit(host = 'h') {
  fireEvent.change(screen.getByPlaceholderText('192.168.1.10'), {
    target: { value: host },
  })
  const btn = screen.getByRole('button', { name: /webdelivery\.build$/ })
  await waitFor(() => expect(btn).not.toBeDisabled())
  fireEvent.click(btn)
}
beforeEach(() => {
  vi.clearAllMocks()
  mockedApi.implantProfiles.mockResolvedValue({ profiles: [PROFILE] })
  mockedApi.webDeliveryFormats.mockResolvedValue({ formats: FORMATS })
  mockedApi.websites.mockResolvedValue({ websites: [] })
})

describe('WebDeliveryPage', () => {
  it('loads profiles and formats on mount', async () => {
    render(<WebDeliveryPage />)
    await waitFor(() => expect(mockedApi.implantProfiles).toHaveBeenCalled())
    expect(mockedApi.webDeliveryFormats).toHaveBeenCalled()
  })

  it('preselects the first profile', async () => {
    render(<WebDeliveryPage />)
    const select = await screen.findByDisplayValue('win-shellcode')
    expect(select).toBeTruthy()
  })

  it('disables the build button until a host and port are given', async () => {
    render(<WebDeliveryPage />)
    const btn = screen.getByRole('button', { name: /webdelivery\.build$/ })
    expect(btn).toBeDisabled()

    fireEvent.change(screen.getByPlaceholderText('192.168.1.10'), {
      target: { value: '10.0.0.5' },
    })
    await waitFor(() => expect(btn).not.toBeDisabled())
  })

  it('sends the request and renders the command and URL', async () => {
    mockedApi.webDelivery.mockResolvedValue({
      command: 'powershell -nop -w hidden -c "..."',
      url: 'http://10.0.0.5:8443/stage.woff',
      job_id: 7,
      warning: '',
    })

    render(<WebDeliveryPage />)
    await submit('10.0.0.5')

    await waitFor(() => expect(mockedApi.webDelivery).toHaveBeenCalled())
    expect(mockedApi.webDelivery).toHaveBeenCalledWith(
      expect.objectContaining({
        profile_name: 'win-shellcode',
        host: '10.0.0.5',
        port: 8443,
        format: 'psh',
      }),
    )

    expect(
      await screen.findByDisplayValue('http://10.0.0.5:8443/stage.woff'),
    ).toBeTruthy()
    expect(
      screen.getByDisplayValue('powershell -nop -w hidden -c "..."'),
    ).toBeTruthy()
  })

  it('surfaces a backend warning instead of reporting success', async () => {
    mockedApi.webDelivery.mockResolvedValue({
      command: 'cmd',
      url: 'http://h/s',
      job_id: 0,
      warning: 'stage published but no listener was started',
    })

    render(<WebDeliveryPage />)
    await submit('h')

    await waitFor(() =>
      expect(mockToast.push).toHaveBeenCalledWith(
        'error',
        'stage published but no listener was started',
      ),
    )
    // An existing listener serving the port is not an error, but the operator
    // must be told that nothing new was started.
    expect(await screen.findByText(/webdelivery\.jobExisting/)).toBeTruthy()
  })

  it('shows the listener job id when one was started', async () => {
    mockedApi.webDelivery.mockResolvedValue({
      command: 'cmd',
      url: 'http://h/s',
      job_id: 12,
      warning: '',
    })

    render(<WebDeliveryPage />)
    await submit('h')

    const text = await screen.findByText(/webdelivery\.jobStarted/)
    expect(text.textContent).toContain('12')
  })

  it('reports a request failure without losing the form', async () => {
    mockedApi.webDelivery.mockRejectedValue(new Error('implant profile "x" not found'))

    render(<WebDeliveryPage />)
    await submit('h')

    expect(await screen.findByText(/implant profile "x" not found/)).toBeTruthy()
    // The button is usable again, so a corrected request can be submitted.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /webdelivery\.build$/ })).not.toBeDisabled(),
    )
  })

  it('keeps working when the profile list cannot be read', async () => {
    mockedApi.implantProfiles.mockRejectedValue(new Error('server unreachable'))

    render(<WebDeliveryPage />)
    expect(await screen.findByText(/server unreachable/)).toBeTruthy()
    // Formats still loaded, because the two requests fail independently.
    expect(mockedApi.webDeliveryFormats).toHaveBeenCalled()
  })

  it('lists the payloads that are already published', async () => {
    mockedApi.websites.mockResolvedValue({
      websites: [
        {
          Name: 'webdelivery',
          Size: 2048,
          Contents: {
            '/stage-windows.woff': {
              Path: '/stage-windows.woff',
              ContentType: 'application/octet-stream',
              Size: 2048,
            },
          },
        },
      ],
    })

    render(<WebDeliveryPage />)

    expect(await screen.findByText('/stage-windows.woff')).toBeTruthy()
    expect(screen.getByText('webdelivery')).toBeTruthy()
  })

  it('names each published payload by its stage name, not the website', async () => {
    mockedApi.websites.mockResolvedValue({
      websites: [
        {
          Name: 'webdelivery',
          Size: 4096,
          Contents: {
            '/stage-linux.woff': {
              Path: '/stage-linux.woff',
              ContentType: 'application/octet-stream',
              Size: 2048,
            },
            '/stage-windows.woff': {
              Path: '/stage-windows.woff',
              ContentType: 'application/octet-stream',
              Size: 2048,
            },
          },
        },
      ],
    })

    render(<WebDeliveryPage />)

    // Every row used to show the website name in the name column, so two
    // different stages were indistinguishable at a glance.
    expect(await screen.findByText('stage-linux.woff')).toBeTruthy()
    expect(screen.getByText('stage-windows.woff')).toBeTruthy()
    // The website is still listed, in its own column.
    expect(screen.getAllByText('webdelivery').length).toBeGreaterThan(0)
  })

  it('shows an empty state before anything is published', async () => {
    render(<WebDeliveryPage />)
    expect(await screen.findByText('webdelivery.publishedEmpty')).toBeTruthy()
  })

  it('refreshes the published list after a build', async () => {
    mockedApi.webDelivery.mockResolvedValue({
      command: 'cmd',
      url: 'http://h/s',
      job_id: 3,
      warning: '',
      website: 'webdelivery',
    })

    render(<WebDeliveryPage />)
    await waitFor(() => expect(mockedApi.websites).toHaveBeenCalledTimes(1))
    await submit('h')
    await waitFor(() => expect(mockedApi.websites).toHaveBeenCalledTimes(2))
  })
})
