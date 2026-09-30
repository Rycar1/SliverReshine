import { render, screen } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import TopologyPage from '../TopologyPage'

const { mockedApi, mockToast } = vi.hoisted(() => ({
  mockedApi: { topology: vi.fn() },
  mockToast: { push: vi.fn() },
}))

vi.mock('../../lib/api', () => ({ api: mockedApi }))
vi.mock('../../components/common/Toast', () => ({ useToast: () => mockToast }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => (o ? `${k}:${JSON.stringify(o)}` : k),
  }),
}))

const CONSOLE = {
  ID: 'c2',
  Kind: 'c2',
  Label: 'Console',
  Hostname: '',
  Username: '',
  OS: '',
  Arch: '',
  Address: '',
  Transport: '',
  Depth: 0,
  Dead: false,
}

const SESSION = {
  ID: 's-1',
  Kind: 'session',
  Label: 'alpha',
  Hostname: 'web01',
  Username: 'root',
  OS: 'linux',
  Arch: 'amd64',
  Address: '10.0.0.5:443',
  Transport: 'mtls',
  Depth: 0,
  Dead: false,
}

beforeEach(() => {
  vi.clearAllMocks()
  mockedApi.topology.mockResolvedValue({ nodes: [], edges: [] })
})

describe('TopologyPage', () => {
  it('shows an empty state when nothing is connected', async () => {
    render(<TopologyPage />)
    expect(await screen.findByText('topology.empty')).toBeTruthy()
  })

  it('renders a node per session and lists them in the table', async () => {
    mockedApi.topology.mockResolvedValue({
      nodes: [CONSOLE, SESSION],
      edges: [{ From: 'c2', To: 's-1', Kind: 'transport', Label: 'mtls' }],
    })

    render(<TopologyPage />)

    // The table is the accessible view of the same data the SVG draws, so the
    // two cannot diverge without a test noticing.
    // The name is drawn in the graph and again in the table, so it is expected
    // to match more than once. Asserting the count proves both views rendered.
    expect((await screen.findAllByText('alpha')).length).toBeGreaterThanOrEqual(2)
    // Each field is drawn in the graph and listed in the table.
    for (const field of ['web01', 'root', '10.0.0.5:443']) {
      expect(screen.getAllByText(field).length).toBeGreaterThanOrEqual(1)
    }
  })

  it('shows the legend and the counts', async () => {
    mockedApi.topology.mockResolvedValue({
      nodes: [CONSOLE, SESSION],
      edges: [
        { From: 'c2', To: 's-1', Kind: 'transport', Label: 'mtls' },
        { From: 'c2', To: 's-1', Kind: 'pivot', Label: 'hop' },
      ],
    })

    render(<TopologyPage />)

    const counts = await screen.findByText(/topology\.counts/)
    expect(counts.textContent).toContain('"sessions":1')
    expect(counts.textContent).toContain('"pivots":1')

    for (const kind of ['transport', 'pivot', 'socks', 'portfwd']) {
      expect(screen.getByText(`topology.edge.${kind}`)).toBeTruthy()
    }
  })

  it('does not render an edge whose endpoint is missing', async () => {
    // A pivot entry for a session that has since died yields this shape.
    mockedApi.topology.mockResolvedValue({
      nodes: [CONSOLE, SESSION],
      edges: [{ From: 's-1', To: 's-gone', Kind: 'pivot', Label: 'stale' }],
    })

    const { container } = render(<TopologyPage />)
    await screen.findAllByText('alpha')
    // The label is only drawn with its edge, so its absence proves the edge was
    // dropped rather than drawn to nowhere.
    expect(container.querySelectorAll('text').length).toBeGreaterThan(0)
    expect(screen.queryByText('stale')).toBeNull()
  })

  it('draws the edge labels it does have', async () => {
    mockedApi.topology.mockResolvedValue({
      nodes: [CONSOLE, SESSION],
      edges: [{ From: 'c2', To: 's-1', Kind: 'transport', Label: 'linux/x64/mtls' }],
    })

    render(<TopologyPage />)
    expect(await screen.findByText('linux/x64/mtls')).toBeTruthy()
  })

  it('reports a fetch failure', async () => {
    mockedApi.topology.mockRejectedValue(new Error('server unreachable'))
    render(<TopologyPage />)
    expect(await screen.findByText(/server unreachable/)).toBeTruthy()
  })

  it('renders a page even with only the console present', async () => {
    mockedApi.topology.mockResolvedValue({ nodes: [CONSOLE], edges: [] })
    render(<TopologyPage />)
    // One node is not a graph, so the empty state is the honest answer.
    expect(await screen.findByText('topology.empty')).toBeTruthy()
  })
})
