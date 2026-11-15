import { useId } from 'react'
import { clampPct } from '@/lib/utils'
import type { HourlyPoint } from '@/types/api'

interface SemiCircularGaugeProps {
  value: number
  label: string
}

export function SemiCircularGauge({ value, label }: SemiCircularGaugeProps) {
  const gradId = useId()
  const pct = clampPct(value)
  const cx = 100
  const cy = 96
  const r = 74
  const theta = Math.PI * (1 - pct / 100)
  const x = cx + r * Math.cos(theta)
  const y = cy - r * Math.sin(theta)
  const arcPath = `M ${cx - r} ${cy} A ${r} ${r} 0 0 1 ${x.toFixed(2)} ${y.toFixed(2)}`

  return (
    <div className="relative w-full max-w-[230px] mx-auto pt-2 flex flex-col items-center">
      <svg viewBox="0 0 200 112" className="w-full h-auto overflow-visible">
        <defs>
          <linearGradient id={gradId} x1="0%" y1="100%" x2="100%" y2="0%">
            <stop offset="0%" stopColor="#3B3B45" />
            <stop offset="55%" stopColor="#9A9AA8" />
            <stop offset="100%" stopColor="#FFFFFF" />
          </linearGradient>
        </defs>
        <path
          d={`M ${cx - r} ${cy} A ${r} ${r} 0 0 1 ${cx + r} ${cy}`}
          fill="none"
          stroke="#25252D"
          strokeWidth="12"
          strokeLinecap="round"
        />
        {pct > 0 && (
          <path
            d={arcPath}
            fill="none"
            stroke={`url(#${gradId})`}
            strokeWidth="12"
            strokeLinecap="round"
          />
        )}
        <circle cx={x} cy={y} r="7" fill="#FFFFFF" stroke="#0B0B0D" strokeWidth="2.5" />
      </svg>
      <div className="-mt-11 text-center">
        <div className="text-[28px] font-semibold tracking-tight text-white tabular-nums leading-none">
          {Math.round(pct)}%
        </div>
        <div className="text-[11px] text-[#7A7A86] mt-1">{label}</div>
      </div>
    </div>
  )
}

interface BarcodeStripProps {
  pct: number
  tone?: 'bright' | 'medium' | 'muted'
}

export function BarcodeStrip({ pct, tone = 'medium' }: BarcodeStripProps) {
  const safePct = clampPct(pct)
  const color =
    tone === 'bright'
      ? 'rgba(244, 244, 246, 0.85)'
      : tone === 'medium'
        ? 'rgba(160, 160, 171, 0.65)'
        : 'rgba(107, 107, 118, 0.45)'

  return (
    <div className="h-[22px] w-full rounded-sm overflow-hidden bg-[#151519]">
      <div
        className="h-full transition-all duration-300"
        style={{
          width: `${safePct}%`,
          backgroundImage: `repeating-linear-gradient(to right, ${color} 0px, ${color} 1.5px, transparent 1.5px, transparent 4px)`,
        }}
      />
    </div>
  )
}

interface WaveCombChartProps {
  series: HourlyPoint[]
  rightBaselineLabel?: string
}

function buildMonotonePath(points: Array<{ x: number; y: number }>): string {
  if (points.length === 0) return ''
  if (points.length === 1) return `M ${points[0].x} ${points[0].y}`
  let d = `M ${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)}`
  for (let i = 0; i < points.length - 1; i++) {
    const p0 = points[Math.max(0, i - 1)]
    const p1 = points[i]
    const p2 = points[i + 1]
    const p3 = points[Math.min(points.length - 1, i + 2)]
    const cp1x = p1.x + (p2.x - p0.x) / 6
    const cp1y = p1.y + (p2.y - p0.y) / 6
    const cp2x = p2.x - (p3.x - p1.x) / 6
    const cp2y = p2.y - (p3.y - p1.y) / 6
    d += ` C ${cp1x.toFixed(2)} ${cp1y.toFixed(2)}, ${cp2x.toFixed(2)} ${cp2y.toFixed(2)}, ${p2.x.toFixed(2)} ${p2.y.toFixed(2)}`
  }
  return d
}

export function WaveCombChart({ series, rightBaselineLabel = 'Now' }: WaveCombChartProps) {
  const uid = useId()
  const safeSeries =
    series.length >= 2
      ? series
      : [
          { label: '-5h', value: 0 },
          { label: '-4h', value: 0 },
          { label: '-3h', value: 0 },
          { label: '-2h', value: 0 },
          { label: '-1h', value: 0 },
          { label: rightBaselineLabel, value: 0 },
        ]

  const width = 460
  const height = 104
  const padTop = 10
  const baselineY = 82
  const cursorX = width - 88
  const maxVal = Math.max(100, ...safeSeries.map((pt) => pt.value))

  const coords = safeSeries.map((pt, idx) => {
    const t = idx / (safeSeries.length - 1)
    const x = 4 + t * (cursorX - 4)
    const norm = Math.max(0, pt.value) / maxVal
    const y = baselineY - norm * (baselineY - padTop)
    return { x, y, label: pt.label, value: pt.value }
  })

  const curvePath = buildMonotonePath(coords)
  const last = coords[coords.length - 1]
  const areaPath = `${curvePath} L ${last.x.toFixed(2)} ${baselineY} L ${coords[0].x.toFixed(2)} ${baselineY} Z`

  return (
    <div className="w-full pt-2">
      <svg viewBox={`0 0 ${width} ${height}`} className="w-full h-[108px] overflow-visible">
        <defs>
          <pattern id={`comb-${uid}`} width="5" height="80" patternUnits="userSpaceOnUse">
            <line x1="1" y1="0" x2="1" y2="80" stroke="#4D4D5A" strokeWidth="1" />
          </pattern>
        </defs>

        <line x1="0" y1={baselineY} x2={width} y2={baselineY} stroke="#23232B" strokeWidth="1" />

        <path d={areaPath} fill={`url(#comb-${uid})`} opacity="0.85" />
        <path d={curvePath} fill="none" stroke="#F4F4F6" strokeWidth="1.75" />

        <rect
          x={cursorX + 4}
          y={baselineY - 8}
          width={width - cursorX - 4}
          height="8"
          fill={`url(#comb-${uid})`}
          opacity="0.7"
        />

        <line
          x1={cursorX}
          y1="4"
          x2={cursorX}
          y2={baselineY}
          stroke="#FFFFFF"
          strokeWidth="1.5"
        />
        <circle cx={last.x} cy={last.y} r="3.5" fill="#FFFFFF" />

        {coords.map((pt, i) => (
          <text
            key={`${pt.label}-${i}`}
            x={pt.x}
            y={height - 2}
            textAnchor={i === 0 ? 'start' : 'middle'}
            fill="#6B6B76"
            fontSize="10"
            className="tabular-nums"
          >
            {pt.label}
          </text>
        ))}
        <text
          x={width - 4}
          y={height - 2}
          textAnchor="end"
          fill="#6B6B76"
          fontSize="10"
          className="tabular-nums"
        >
          {rightBaselineLabel}
        </text>
      </svg>
    </div>
  )
}
