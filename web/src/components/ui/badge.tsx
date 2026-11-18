import type { HTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: 'default' | 'breaking' | 'heartbeat' | 'muted' | 'tier1' | 'tier2' | 'tier3'
}

const BADGE_CLASSES: Record<NonNullable<BadgeProps['variant']>, string> = {
  default: 'bg-[#1D1D22] text-[#A0A0AB] border border-[#2A2A32]',
  breaking: 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D] font-semibold border border-[#E4E4EA]',
  heartbeat: 'bg-[#1C1C22] text-[#D4D4DC] border border-[#2E2E38]',
  muted: 'bg-[#18181C] text-[#6B6B76] border border-[#24242C]',
  tier1: 'bg-[#363642] text-[#F4F4F6] font-semibold border border-[#4A4A58]',
  tier2: 'bg-[#22222A] text-[#C8C8D2] border border-[#34343F]',
  tier3: 'bg-[#18181C] text-[#7A7A86] border border-[#24242C]',
}

export function Badge({ variant = 'default', className, ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] tabular-nums whitespace-nowrap',
        BADGE_CLASSES[variant],
        className,
      )}
      {...props}
    />
  )
}
