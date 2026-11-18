import type { ButtonHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'outline'
  size?: 'sm' | 'md' | 'icon'
}

const VARIANT_CLASSES: Record<NonNullable<ButtonProps['variant']>, string> = {
  primary: 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D] hover:from-white hover:to-[#E2E2E8] font-semibold',
  secondary: 'bg-[#1D1D22] hover:bg-[#24242C] text-[#F4F4F6] border border-[#2C2C34]',
  outline: 'bg-[#18181C] hover:bg-[#202026] text-[#F4F4F6] border border-[#24242C]',
  ghost: 'bg-transparent hover:bg-[#1E1E24] text-[#9A9AA6] hover:text-white',
  danger: 'bg-[#2A1C1E] hover:bg-[#3A2225] text-[#FCA5A5] border border-[#4A252A]',
}

const SIZE_CLASSES: Record<NonNullable<ButtonProps['size']>, string> = {
  sm: 'h-8 px-3 text-[12px] rounded-xl',
  md: 'h-9 px-3.5 text-[13px] rounded-xl',
  icon: 'h-9 w-9 rounded-full',
}

export function Button({
  variant = 'secondary',
  size = 'md',
  className,
  type = 'button',
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      className={cn(
        'inline-flex items-center justify-center gap-2 transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed',
        VARIANT_CLASSES[variant],
        SIZE_CLASSES[size],
        className,
      )}
      {...props}
    />
  )
}
