import type { HTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        'bg-[#18181C] border border-[#22222A] rounded-[22px]',
        className,
      )}
      {...props}
    />
  )
}
