import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatUtcTime(iso?: string): string {
  if (!iso) return '--:-- UTC'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '--:-- UTC'
  const hh = String(date.getUTCHours()).padStart(2, '0')
  const mm = String(date.getUTCMinutes()).padStart(2, '0')
  return `${hh}:${mm} UTC`
}

export function formatRelativeTime(iso?: string): string {
  if (!iso) return 'never'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return 'unknown'
  const diffSec = Math.round((Date.now() - date.getTime()) / 1000)
  if (diffSec < 0) {
    const ahead = Math.abs(diffSec)
    if (ahead < 60) return `in ${ahead}s`
    if (ahead < 3600) return `in ${Math.floor(ahead / 60)}m`
    return `in ${Math.floor(ahead / 3600)}h`
  }
  if (diffSec < 60) return `${diffSec}s ago`
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`
  return `${Math.floor(diffSec / 86400)}d ago`
}

export function formatCountdown(seconds: number): string {
  const safe = Math.max(0, Math.floor(seconds))
  const m = Math.floor(safe / 60)
  const s = safe % 60
  return `${String(m).padStart(2, '0')}m ${String(s).padStart(2, '0')}s`
}

export function tierLabel(tier: number): string {
  if (tier === 1) return 'T1'
  if (tier === 2) return 'T2'
  return `T${Math.max(3, tier || 3)}`
}

export function clampPct(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100, value))
}
