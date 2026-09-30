import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import type { TopologyEdge, TopologyGraph, TopologyNode } from '../lib/types'
import { useToast } from '../components/common/Toast'
import './pages.css'

/**
 * Column geometry. Vertices are placed in columns by pivot depth, which makes a
 * chain read left-to-right without running a force simulation: the depth is
 * already known from the console's pivot tree, so a deterministic layout is both
 * simpler and more stable than one that reshuffles on every poll.
 */
const COL_W = 210
const ROW_H = 78
const PAD_X = 70
const PAD_Y = 54
const NODE_W = 150
const NODE_H = 48

interface Placed {
  node: TopologyNode
  x: number
  y: number
}

/**
 * Layout assigns each vertex a column (its depth) and a row within it. Nodes of
 * equal depth are stacked in id order, so the picture is identical between two
 * polls of an unchanged network.
 */
function layout(nodes: TopologyNode[]): Placed[] {
  const byDepth = new Map<number, TopologyNode[]>()
  for (const n of nodes) {
    const d = n.Kind === 'c2' ? 0 : Math.max(n.Depth, 1)
    const bucket = byDepth.get(d)
    if (bucket) bucket.push(n)
    else byDepth.set(d, [n])
  }

  const placed: Placed[] = []
  const depths = [...byDepth.keys()].sort((a, b) => a - b)
  for (const d of depths) {
    const bucket = byDepth.get(d)!.slice().sort((a, b) => a.ID.localeCompare(b.ID))
    bucket.forEach((node, row) => {
      placed.push({
        node,
        x: PAD_X + d * COL_W,
        y: PAD_Y + row * ROW_H,
      })
    })
  }
  return placed
}

/** Edge stroke and dash per relationship type, so kinds are distinguishable. */
function edgeStyle(kind: string): { stroke: string; dash?: string } {
  switch (kind) {
    case 'pivot':
      return { stroke: '#c05cff', dash: '7 5' }
    case 'socks':
      return { stroke: '#3fbf7f' }
    case 'portfwd':
      return { stroke: '#e0a33a', dash: '2 4' }
    default:
      return { stroke: '#2fa8d8' }
  }
}

function kindClass(kind: string): string {
  switch (kind) {
    case 'beacon':
      return 'topo-node beacon'
    case 'c2':
      return 'topo-node c2'
    default:
      return 'topo-node session'
  }
}

export default function TopologyPage() {
  const { t } = useTranslation()
  const toast = useToast()
  const [graph, setGraph] = useState<TopologyGraph | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setGraph(await api.topology())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const placed = useMemo(() => layout(graph?.nodes || []), [graph])
  const positions = useMemo(() => {
    const m = new Map<string, Placed>()
    for (const p of placed) m.set(p.node.ID, p)
    return m
  }, [placed])

  const width = useMemo(() => {
    if (placed.length === 0) return 600
    return Math.max(...placed.map((p) => p.x)) + NODE_W + PAD_X
  }, [placed])
  const height = useMemo(() => {
    if (placed.length === 0) return 300
    return Math.max(...placed.map((p) => p.y)) + NODE_H + PAD_Y
  }, [placed])

  const counts = useMemo(() => {
    const nodes = graph?.nodes || []
    return {
      sessions: nodes.filter((n) => n.Kind === 'session').length,
      beacons: nodes.filter((n) => n.Kind === 'beacon').length,
      pivots: (graph?.edges || []).filter((e) => e.Kind === 'pivot').length,
    }
  }, [graph])

  const copyLabel = (e: TopologyEdge) => {
    navigator.clipboard?.writeText(e.Label)
    toast.push('success', t('topology.copied', { label: e.Label }))
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <div className="page-title">{t('nav.topology')}</div>
          <div className="page-sub">{t('topology.sub')}</div>
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <span className="badge gray mono">
            {t('topology.counts', counts)}
          </span>
          <button className="btn" onClick={load} disabled={loading}>
            {loading ? t('topology.loading') : t('topology.refresh')}
          </button>
        </div>
      </div>

      {error && <div className="card error">{error}</div>}

      {!error && placed.length <= 1 && !loading && (
        <div className="empty">{t('topology.empty')}</div>
      )}

      {placed.length > 1 && (
        <div className="card topo-card">
          <div className="topo-legend">
            {(['transport', 'pivot', 'socks', 'portfwd'] as const).map((k) => (
              <span key={k} className="topo-legend-item">
                <svg width="26" height="10" aria-hidden>
                  <line
                    x1="0"
                    y1="5"
                    x2="26"
                    y2="5"
                    stroke={edgeStyle(k).stroke}
                    strokeWidth="2"
                    strokeDasharray={edgeStyle(k).dash}
                  />
                </svg>
                {t(`topology.edge.${k}`)}
              </span>
            ))}
          </div>

          <div className="topo-scroll">
            <svg
              width={width}
              height={height}
              viewBox={`0 0 ${width} ${height}`}
              role="img"
              aria-label={t('nav.topology')}
            >
              <defs>
                {(['transport', 'pivot', 'socks', 'portfwd'] as const).map((k) => (
                  <marker
                    key={k}
                    id={`arrow-${k}`}
                    viewBox="0 0 10 10"
                    refX="9"
                    refY="5"
                    markerWidth="6"
                    markerHeight="6"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 0 L 10 5 L 0 10 z" fill={edgeStyle(k).stroke} />
                  </marker>
                ))}
              </defs>

              {(graph?.edges || []).map((e, i) => {
                const a = positions.get(e.From)
                const b = positions.get(e.To)
                if (!a || !b) return null
                // Draw edge-to-edge rather than centre-to-centre so the arrow
                // tip meets the box instead of disappearing under it.
                const x1 = a.x + NODE_W
                const y1 = a.y + NODE_H / 2
                const x2 = b.x
                const y2 = b.y + NODE_H / 2
                const mid = (x1 + x2) / 2
                return (
                  <g key={`${e.From}-${e.To}-${e.Kind}-${i}`}>
                    <path
                      d={`M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${x2} ${y2}`}
                      fill="none"
                      stroke={edgeStyle(e.Kind).stroke}
                      strokeWidth="1.8"
                      strokeDasharray={edgeStyle(e.Kind).dash}
                      markerEnd={`url(#arrow-${e.Kind})`}
                    />
                    <text
                      x={mid}
                      y={(y1 + y2) / 2 - 5}
                      textAnchor="middle"
                      className="topo-edge-label"
                      onClick={() => copyLabel(e)}
                    >
                      {e.Label}
                    </text>
                  </g>
                )
              })}

              {placed.map(({ node, x, y }) => (
                <g key={node.ID} transform={`translate(${x}, ${y})`} className="topo-group">
                  <title>
                    {[
                      node.Label,
                      node.Hostname && `${t('topology.host')}: ${node.Hostname}`,
                      node.Username && `${t('topology.user')}: ${node.Username}`,
                      node.Address && `${t('topology.address')}: ${node.Address}`,
                      node.Transport && `${t('topology.transport')}: ${node.Transport}`,
                    ]
                      .filter(Boolean)
                      .join('\n')}
                  </title>
                  <rect
                    width={NODE_W}
                    height={NODE_H}
                    rx="7"
                    className={`${kindClass(node.Kind)}${node.Dead ? ' dead' : ''}`}
                  />
                  <text x="10" y="19" className="topo-node-title">
                    {truncate(node.Label, 18)}
                  </text>
                  <text x="10" y="35" className="topo-node-sub">
                    {truncate(node.Username || node.Address || node.Kind, 22)}
                  </text>
                  {node.Depth > 0 && (
                    <text x={NODE_W - 8} y="14" textAnchor="end" className="topo-node-depth">
                      {`h${node.Depth}`}
                    </text>
                  )}
                </g>
              ))}
            </svg>
          </div>
        </div>
      )}

      {placed.length > 1 && (
        <div className="card">
          <table className="data">
            <thead>
              <tr>
                <th>{t('topology.thNode')}</th>
                <th>{t('topology.thKind')}</th>
                <th>{t('topology.thHost')}</th>
                <th>{t('topology.thUser')}</th>
                <th>{t('topology.thTransport')}</th>
                <th>{t('topology.thAddress')}</th>
              </tr>
            </thead>
            <tbody>
              {placed.map(({ node }) => (
                <tr key={node.ID}>
                  <td className="mono">{node.Label}</td>
                  <td>
                    <span className="badge gray">{t(`topology.kind.${node.Kind}`)}</span>
                  </td>
                  <td className="mono">{node.Hostname || '-'}</td>
                  <td className="mono">{node.Username || '-'}</td>
                  <td className="mono">{node.Transport || '-'}</td>
                  <td className="mono">{node.Address || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function truncate(s: string, n: number): string {
  if (!s) return ''
  return s.length > n ? `${s.slice(0, n - 1)}…` : s
}
