import type { ReactNode } from 'react'
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import {
  Activity,
  Bell,
  LayoutDashboard,
  Newspaper,
  Rss,
  Search,
  Settings,
} from 'lucide-react'
import { cn } from '@/lib/utils'

interface NavItem {
  to: string
  label: string
  icon: typeof LayoutDashboard
}

const PRIMARY_NAV: NavItem[] = [
  { to: '/', label: 'Overview', icon: LayoutDashboard },
  { to: '/intelligence', label: 'Intelligence', icon: Activity },
  { to: '/raw-feed', label: 'Raw Feed', icon: Newspaper },
  { to: '/sources', label: 'Sources', icon: Rss },
]

interface ShellProps {
  children: ReactNode
  searchValue: string
  onSearchChange: (val: string) => void
  searchPlaceholder?: string
}

export function Shell({
  children,
  searchValue,
  onSearchChange,
  searchPlaceholder = 'Search clusters, sources, headlines...',
}: ShellProps) {
  const location = useLocation()
  const navigate = useNavigate()

  const isDispatch = location.pathname === '/dispatch'
  const isSettings = location.pathname === '/settings'

  return (
    <div className="min-h-screen w-full bg-[#0B0B0D] text-[#F4F4F6] flex flex-col">
      <div className="w-full max-w-[1680px] mx-auto px-4 sm:px-6 lg:px-8 py-4 sm:py-6 flex-1 flex flex-col gap-6">
        <header className="w-full bg-[#121215] border border-[#22222A] rounded-[24px] px-4 sm:px-5 py-3 flex flex-wrap items-center justify-between gap-3">
          <div className="w-full md:w-auto flex items-center justify-between md:justify-start gap-2 md:gap-3 min-w-0">
            <button
              type="button"
              onClick={() => navigate('/')}
              className="flex items-center gap-2.5 pr-1 cursor-pointer shrink-0"
            >
              <span className="w-9 h-9 rounded-full bg-[#1D1D22] border border-[#2C2C35] overflow-hidden flex items-center justify-center shrink-0">
                <img src="/logo.jpg" alt="Celerum" className="w-full h-full object-cover" />
              </span>
              <span className="hidden sm:inline text-[15px] font-semibold tracking-tight text-white">
                Celerum
              </span>
            </button>

            <nav
              aria-label="Primary navigation"
              className="flex-1 md:flex-initial flex items-center justify-evenly md:justify-start md:gap-1.5"
            >
              {PRIMARY_NAV.map((item) => {
                const Icon = item.icon
                return (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.to === '/'}
                    title={item.label}
                    aria-label={item.label}
                    className={({ isActive }) =>
                      cn(
                        'transition-colors flex items-center justify-center',
                        'w-10 h-10 rounded-full md:w-auto md:h-auto md:px-4 md:py-2 md:rounded-full md:gap-2 text-[13px] font-medium',
                        isActive
                          ? 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D] font-semibold shadow-sm'
                          : 'bg-[#18181C] md:bg-transparent text-[#8B8B96] hover:text-white hover:bg-[#1E1E24]',
                      )
                    }
                  >
                    <Icon className="w-4 h-4 md:hidden" />
                    <span className="hidden md:inline">{item.label}</span>
                  </NavLink>
                )
              })}
            </nav>
          </div>

          <div className="flex items-center gap-2.5 flex-1 sm:flex-initial justify-end">
            <div className="relative flex-1 sm:w-64 md:w-72">
              <Search className="w-4 h-4 text-[#6B6B76] absolute left-3.5 top-1/2 -translate-y-1/2 pointer-events-none" />
              <input
                type="text"
                value={searchValue}
                onChange={(e) => onSearchChange(e.target.value)}
                placeholder={searchPlaceholder}
                className="w-full h-9 bg-[#18181C] border border-[#24242C] rounded-full pl-9 pr-3.5 text-[13px] text-[#F4F4F6] placeholder-[#6B6B76] focus:outline-none focus:border-[#4A4A58]"
              />
            </div>

            <button
              type="button"
              onClick={() => navigate('/dispatch')}
              title="Dispatch & Retry Queue"
              aria-label="Dispatch & Retry Queue"
              className={cn(
                'w-9 h-9 rounded-full border flex items-center justify-center transition-colors cursor-pointer shrink-0',
                isDispatch
                  ? 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D] border-[#E4E4EA]'
                  : 'bg-[#18181C] border-[#24242C] text-[#9A9AA6] hover:text-white hover:bg-[#22222A]',
              )}
            >
              <Bell className="w-4 h-4" />
            </button>

            <button
              type="button"
              onClick={() => navigate('/settings')}
              title="Settings & Doctor"
              aria-label="Settings & Doctor"
              className={cn(
                'w-9 h-9 rounded-full border flex items-center justify-center transition-colors cursor-pointer shrink-0',
                isSettings
                  ? 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D] border-[#E4E4EA]'
                  : 'bg-[#18181C] border-[#24242C] text-[#9A9AA6] hover:text-white hover:bg-[#22222A]',
              )}
            >
              <Settings className="w-4 h-4" />
            </button>
          </div>
        </header>

        <main className="flex-1 flex flex-col gap-6">{children}</main>
      </div>
    </div>
  )
}
