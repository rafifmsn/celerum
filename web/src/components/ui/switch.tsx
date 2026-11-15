import { cn } from '@/lib/utils'

interface SwitchProps {
  checked: boolean
  onCheckedChange: (next: boolean) => void
  disabled?: boolean
  label?: string
}

export function Switch({ checked, onCheckedChange, disabled, label }: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onCheckedChange(!checked)}
      className={cn(
        'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed',
        checked ? 'bg-white border-white' : 'bg-[#121215] border-[#2C2C35]',
      )}
    >
      <span
        className={cn(
          'inline-block h-4 w-4 transform rounded-full transition-transform',
          checked ? 'translate-x-5.5 bg-[#0B0B0D]' : 'translate-x-1 bg-[#6B6B76]',
        )}
      />
    </button>
  )
}
