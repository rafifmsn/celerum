import type { InputHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        'w-full bg-[#121215] border border-[#24242C] rounded-xl px-3 py-2 text-[13px] text-[#F4F4F6] placeholder-[#6B6B76] focus:outline-none focus:border-[#4A4A58] transition-colors',
        className,
      )}
      {...props}
    />
  )
}
