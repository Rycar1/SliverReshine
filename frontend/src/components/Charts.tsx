import type { ReactNode } from 'react'

export interface ChartDatum {
  label: string
  value: number
  color: string
}

export interface DonutProps {
  data: ChartDatum[]
  size?: number
  thickness?: number
  center: string
  centerSub: string
  emptyText: string
}

export interface BarListProps {
  rows: ChartDatum[]
  emptyText: string
  maxOverride?: number
  labelWidth?: number
  unit?: ReactNode
}

export const CHART_COLORS = ['#7c5cff', '#4cb782', '#6188ff', '#f5b62e', '#f76808', '#e5484d']

const GAP = 1.5

export function Donut({ data, size = 132, thickness = 15, center, centerSub, emptyText }: DonutProps) {
  const total = data.reduce((sum, d) => sum + d.value, 0)
  const r = (size - thickness) / 2
  const circ = 2 * Math.PI * r
  const cx = size / 2
  const cy = size / 2

  if (total === 0) {
    return (
      <div className="viz-empty" style={{ minHeight: size }}>
        {emptyText}
      </div>
    )
  }

  let acc = 0
  const segments = data.map((d) => {
    const arc = (d.value / total) * circ
    const seg = Math.max(arc - GAP, 0.5)
    const offset = -acc
    acc += arc
    return { ...d, seg, offset }
  })

  return (
    <div className="viz-donut" style={{ width: size, height: size }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        <circle cx={cx} cy={cy} r={r} fill="none" stroke="rgba(127,127,127,0.12)" strokeWidth={thickness} />
        {segments.map((s) => (
          <circle
            key={s.label}
            cx={cx}
            cy={cy}
            r={r}
            fill="none"
            stroke={s.color}
            strokeWidth={thickness}
            strokeDasharray={`${s.seg} ${circ - s.seg}`}
            strokeDashoffset={s.offset}
            transform={`rotate(-90 ${cx} ${cy})`}
          />
        ))}
      </svg>
      <div className="viz-donut-center">
        <div className="viz-donut-total mono">{center}</div>
        <div className="viz-donut-sub">{centerSub}</div>
      </div>
    </div>
  )
}

export function BarList({ rows, emptyText, maxOverride, labelWidth = 120, unit }: BarListProps) {
  const max = maxOverride ?? Math.max(1, ...rows.map((r) => r.value))
  if (rows.length === 0) {
    return <div className="viz-empty">{emptyText}</div>
  }
  return (
    <div className="bar-list">
      {rows.map((r) => (
        <div className="bar-row" key={r.label}>
          <div className="bar-label" style={{ width: labelWidth }} title={r.label}>
            {r.label}
          </div>
          <div className="bar-track">
            <div className="bar-fill" style={{ width: `${(r.value / max) * 100}%`, background: r.color }} />
          </div>
          <div className="bar-value">
            {r.value}
            {unit}
          </div>
        </div>
      ))}
    </div>
  )
}

export function Legend({ data }: { data: ChartDatum[] }) {
  return (
    <div className="viz-legend">
      {data.map((d) => (
        <div className="viz-legend-item" key={d.label}>
          <span className="viz-legend-dot" style={{ background: d.color }} />
          <span className="viz-legend-label" title={d.label}>
            {d.label}
          </span>
          <span className="viz-legend-count">{d.value}</span>
        </div>
      ))}
    </div>
  )
}
