import type { SelectHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

export function Select({ className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'bg-[#121215] border border-[#24242C] rounded-xl px-3 py-2 text-[13px] text-[#F4F4F6] focus:outline-none focus:border-[#4A4A58] transition-colors',
        className,
      )}
      {...props}
    >
      {children}
    </select>
  )
}
