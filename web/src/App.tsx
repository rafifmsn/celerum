import { useCallback, useEffect, useState } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { Shell } from '@/components/Shell'
import { fetchOverview } from '@/lib/api'
import { useTelemetryStream } from '@/lib/sse'
import { DispatchPage } from '@/pages/DispatchPage'
import { IntelligencePage } from '@/pages/IntelligencePage'
import { OverviewPage } from '@/pages/OverviewPage'
import { RawFeedPage } from '@/pages/RawFeedPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { SourcesPage } from '@/pages/SourcesPage'
import type { OverviewMetrics } from '@/types/api'

export function App() {
  const location = useLocation()
  const [searchQuery, setSearchQuery] = useState('')
  const [overviewTier, setOverviewTier] = useState('all')
  const [overviewData, setOverviewData] = useState<OverviewMetrics | null>(null)
  const [streamTick, setStreamTick] = useState(0)

  const handleStreamUpdate = useCallback(() => {
    setStreamTick((t) => t + 1)
  }, [])

  useTelemetryStream(handleStreamUpdate)

  const loadOverview = useCallback(async () => {
    try {
      const res = await fetchOverview(overviewTier)
      setOverviewData(res)
    } catch {
      // keep previous metrics on transient polling error
    }
  }, [overviewTier])

  useEffect(() => {
    if (location.pathname === '/') {
      void loadOverview()
    }
  }, [location.pathname, loadOverview, streamTick])

  return (
    <Shell searchValue={searchQuery} onSearchChange={setSearchQuery}>
      <ErrorBoundary resetKey={location.pathname}>
        <Routes>
          <Route
            path="/"
            element={
              <OverviewPage
                data={overviewData}
                tier={overviewTier}
                onTierChange={setOverviewTier}
                searchQuery={searchQuery}
                onRefresh={() => void loadOverview()}
              />
            }
          />
          <Route
            path="/intelligence"
            element={<IntelligencePage searchQuery={searchQuery} streamTick={streamTick} />}
          />
          <Route
            path="/raw-feed"
            element={<RawFeedPage searchQuery={searchQuery} streamTick={streamTick} />}
          />
          <Route
            path="/sources"
            element={<SourcesPage searchQuery={searchQuery} streamTick={streamTick} />}
          />
          <Route
            path="/dispatch"
            element={<DispatchPage searchQuery={searchQuery} streamTick={streamTick} />}
          />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </ErrorBoundary>
    </Shell>
  )
}

export default App
