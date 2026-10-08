import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AITab from '../AITab'
import { api } from '../../../lib/api'

vi.mock('../../../lib/api', () => ({
  api: {
    aiStatus: vi.fn(),
    aiCollect: vi.fn(),
    aiCollectStream: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

const STATUS = {
  configured: true,
  baseURL: 'https://model.example/v1',
  model: 'gpt-test',
  hasKey: true,
  readOnly: true,
  allowlist: ['cat', 'ls'],
}

beforeEach(() => {
  vi.clearAllMocks()
  mockedApi.aiStatus.mockResolvedValue(STATUS as never)
})

describe('AITab', () => {
  it('explains when no provider is configured and offers no collection controls', async () => {
    mockedApi.aiStatus.mockResolvedValue({ ...STATUS, configured: false, hasKey: false } as never)

    render(<AITab sessionId="s1" />)

    expect(await screen.findByText(/No AI provider is configured/)).toBeInTheDocument()
    // The model badge is still shown so the operator can see what would run.
    expect(screen.getByText(/gpt-test/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Run collection' })).not.toBeInTheDocument()
  })

  it('streams a collection and renders steps, refusals, findings and stored items', async () => {
    const steps = [
      { command: 'cat /etc/passwd', thinking: 'the passwd file may name login accounts', output: 'root:x:0:0', status: 0 },
      { command: 'rm -rf /', refused: true, refusal: 'command is not in the read-only allowlist' },
    ]
    const result = {
      steps,
      findings: [
        { kind: 'password', name: 'admin', username: 'admin', secret: 'hunter2', source: 'cat /etc/passwd' },
      ],
      stored: [{ kind: 'credential', name: 'admin', id: 'c-1' }],
      skipped: ['duplicate credential'],
      summary: 'one credential recovered',
      stopped: 'objective met',
      model: 'gpt-test',
      dry_run: false,
    }

    // The run is streamed: the tab renders the step in flight, then replaces it
    // with the finished step as each one closes.
    mockedApi.aiCollectStream.mockImplementation((async (
      _id: string,
      _req: unknown,
      onEvent: (ev: unknown) => void,
    ) => {
      onEvent({ type: 'status', text: 'target platform: linux/amd64' })
      onEvent({ type: 'thinking', index: 0, text: steps[0].thinking })
      onEvent({ type: 'command', index: 0, step: { command: steps[0].command, thinking: steps[0].thinking } })
      onEvent({ type: 'step', index: 0, step: steps[0] })
      onEvent({ type: 'command', index: 1, step: { command: steps[1].command } })
      onEvent({ type: 'step', index: 1, step: steps[1] })
      onEvent({ type: 'summary', text: 'one credential recovered' })
      return result
    }) as never)

    render(<AITab sessionId="s1" />)

    const run = await screen.findByRole('button', { name: 'Run collection' })
    await userEvent.click(run)

    await waitFor(() => expect(mockedApi.aiCollectStream).toHaveBeenCalledTimes(1))
    expect(mockedApi.aiCollectStream.mock.calls[0][0]).toBe('s1')

    // The command text appears both as a step and as the finding's source.
    expect((await screen.findAllByText('cat /etc/passwd')).length).toBeGreaterThan(0)
    expect(screen.getByText('Refused by policy')).toBeInTheDocument()
    expect(screen.getByText(/not in the read-only allowlist/)).toBeInTheDocument()
    // The model's reasoning is shown alongside the command it ran.
    expect(screen.getByText(/the passwd file may name login accounts/)).toBeInTheDocument()
    expect(screen.getByText('hunter2')).toBeInTheDocument()
    expect(screen.getByText(/one credential recovered/)).toBeInTheDocument()
  })
})
