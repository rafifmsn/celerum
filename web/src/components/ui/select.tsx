import type { SelectHTMLAttributes } from 'react'
import { ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'

export function Select({ className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <div className={cn('relative inline-flex items-center min-w-0 max-w-full', className)}>
      <select
        className="w-full min-w-0 appearance-none truncate bg-[#121215] hover:bg-[#16161B] hover:border-[#32323E] border border-[#24242C] rounded-xl pl-3.5 pr-9 py-2 text-[13px] text-[#F4F4F6] focus:outline-none focus:border-[#4A4A58] transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        {...props}
      >
        {children}
      </select>
      <ChevronDown
        aria-hidden="true"
        className="w-4 h-4 text-[#8B8B96] pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 shrink-0"
      />
    </div>
  )
}
