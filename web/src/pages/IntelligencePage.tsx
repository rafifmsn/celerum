import { useCallback, useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, ExternalLink, Layers, Sparkles } from 'lucide-react'
import { BarcodeStrip } from '@/components/Charts'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { fetchClusters } from '@/lib/api'
import { cn, formatRelativeTime, tierLabel } from '@/lib/utils'
import type { ClustersResponse } from '@/types/api'

interface IntelligencePageProps {
  searchQuery: string
  streamTick: number
}

const FILTER_OPTIONS = [
  { id: 'all', label: 'All Clusters' },
  { id: 'breaking', label: 'Breaking' },
  { id: 'tier1', label: 'Tier 1 Lead' },
]

export function IntelligencePage({ searchQuery, streamTick }: IntelligencePageProps) {
  const [filter, setFilter] = useState('all')
  const [tier, setTier] = useState('all')
  const [sort, setSort] = useState('score')
  const [data, setData] = useState<ClustersResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  const load = useCallback(async () => {
    try {
      const res = await fetchClusters({ filter, tier, sort, q: searchQuery })
      setData(res)
    } finally {
      setLoading(false)
    }
  }, [filter, tier, sort, searchQuery])

  useEffect(() => {
    void load()
  }, [load, streamTick])

  const toggleExpand = (hash: string) => {
    setExpanded((prev) => ({ ...prev, [hash]: !prev[hash] }))
  }

  const totalWindow = Math.max(1, data?.total_window_clusters ?? 1)
  const breakingPct = ((data?.breaking_count ?? 0) / totalWindow) * 100
  const heartbeatPct = ((data?.heartbeat_count ?? 0) / totalWindow) * 100
  const belowPct = ((data?.below_threshold_count ?? 0) / totalWindow) * 100

  return (
    <div className="grid grid-cols-1 xl:grid-cols-12 gap-5 items-start">
      <div className="xl:col-span-8 flex flex-col gap-4">
        <Card className="p-5 flex flex-wrap items-center justify-between gap-4">
          <div className="flex flex-wrap items-center gap-2">
            {FILTER_OPTIONS.map((opt) => (
              <button
                key={opt.id}
                type="button"
                onClick={() => setFilter(opt.id)}
                className={cn(
                  'px-3.5 py-1.5 rounded-full text-[12px] font-medium transition-colors cursor-pointer',
                  filter === opt.id
                    ? 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D]'
                    : 'bg-[#121215] border border-[#24242C] text-[#9A9AA6] hover:text-white',
                )}
              >
                {opt.label}
                {opt.id === 'breaking' && ` (>= ${(data?.breaking_threshold ?? 8).toFixed(1)})`}
              </button>
            ))}
          </div>

          <div className="flex items-center gap-2.5">
            <Select value={sort} onChange={(e) => setSort(e.target.value)} aria-label="Sort clusters">
              <option value="score">Highest Score</option>
              <option value="recent">Most Recent</option>
            </Select>
          </div>
        </Card>

        {loading && !data ? (
          <Card className="p-8 text-center text-[13px] text-[#7A7A86]">
            Loading ranked intelligence clusters...
          </Card>
        ) : (data?.clusters ?? []).length === 0 ? (
          <Card className="p-8 text-center text-[13px] text-[#7A7A86]">
            No intelligence clusters match the current filters.
          </Card>
        ) : (
          <div className="flex flex-col gap-3.5">
            {(data?.clusters ?? []).map((cluster) => {
              const isOpen = Boolean(expanded[cluster.cluster_hash])
              const isBreaking = cluster.score >= (data?.breaking_threshold ?? 8)
              return (
                <Card key={cluster.cluster_hash} className="p-5 transition-colors hover:border-[#32323E]">
                  <div className="space-y-2.5">
                    <div className="flex flex-wrap items-center gap-2 text-[11px]">
                      <Badge variant={isBreaking ? 'breaking' : 'heartbeat'}>
                        Score {cluster.score.toFixed(2)}
                      </Badge>
                      <Badge variant={cluster.tier === 1 ? 'tier1' : cluster.tier === 2 ? 'tier2' : 'tier3'}>
                        {tierLabel(cluster.tier)} &middot; {cluster.lead_source || 'Unknown'}
                      </Badge>
                      {cluster.category && <Badge>{cluster.category}</Badge>}
                      {cluster.enriched && (
                        <Badge className="gap-1">
                          <Sparkles className="w-3 h-3" />
                          Synthesized
                        </Badge>
                      )}
                      <span className="text-[#7A7A86] tabular-nums">
                        {formatRelativeTime(cluster.updated_at)}
                      </span>
                    </div>

                    <h3 className="text-[16px] font-semibold text-white leading-snug">
                      {cluster.title}
                    </h3>

                    {cluster.summary && (
                      <p className="text-[13px] text-[#A0A0AB] leading-relaxed">
                        {cluster.summary}
                      </p>
                    )}

                    <div className="pt-1">
                      <button
                        type="button"
                        onClick={() => toggleExpand(cluster.cluster_hash)}
                        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-[#121215] border border-[#24242C] text-[12px] text-[#D4D4DC] hover:bg-[#1E1E25] cursor-pointer tabular-nums"
                      >
                        <Layers className="w-3.5 h-3.5" />
                        <span>{cluster.source_count} sources</span>
                        {isOpen ? (
                          <ChevronDown className="w-3.5 h-3.5" />
                        ) : (
                          <ChevronRight className="w-3.5 h-3.5" />
                        )}
                      </button>
                    </div>
                  </div>

                  {isOpen && (
                    <div className="mt-4 pt-4 border-t border-[#24242C] space-y-2">
                      {(cluster.sources ?? []).length === 0 ? (
                        <div className="text-[12px] text-[#7A7A86]">
                          No source link details recorded for this cluster.
                        </div>
                      ) : (
                        cluster.sources.map((src, idx) => (
                          <a
                            key={`${src.url}-${idx}`}
                            href={src.url}
                            target="_blank"
                            rel="noreferrer"
                            className="flex items-center justify-between gap-3 px-3.5 py-2.5 rounded-xl bg-[#121215] hover:bg-[#1D1D24] border border-[#1E1E26] transition-colors"
                          >
                            <div className="min-w-0">
                              <div className="flex items-center gap-2 text-[11px] text-[#8B8B96]">
                                <span className="font-semibold text-white">
                                  {tierLabel(src.tier)}
                                </span>
                                <span>{src.name}</span>
                                <span className="tabular-nums">
                                  &middot; {formatRelativeTime(src.published_at)}
                                </span>
                              </div>
                              <div className="text-[13px] text-[#E4E4EA] truncate mt-0.5">
                                {src.title}
                              </div>
                            </div>
                            <ExternalLink className="w-4 h-4 text-[#7A7A86] shrink-0" />
                          </a>
                        ))
                      )}
                    </div>
                  )}
                </Card>
              )
            })}

            <div className="pt-2 pb-1 text-center text-[12px] text-[#6B6B76] tabular-nums">
              End of clusters in current {data?.window_duration || '24h'} sliding window
            </div>
          </div>
        )}
      </div>

      <aside className="xl:col-span-4 flex flex-col gap-5 xl:sticky xl:top-6">
        <Card className="p-5 space-y-4">
          <h2 className="text-[14px] font-semibold text-white">Scoring Distribution</h2>

          <div className="space-y-3">
            <div>
              <div className="flex justify-between text-[12px] text-[#D4D4DC] mb-1 tabular-nums">
                <span>Breaking Dispatched</span>
                <span>{data?.breaking_count ?? 0}</span>
              </div>
              <BarcodeStrip pct={breakingPct} tone="bright" />
            </div>
            <div>
              <div className="flex justify-between text-[12px] text-[#A0A0AB] mb-1 tabular-nums">
                <span>Heartbeat Queue</span>
                <span>{data?.heartbeat_count ?? 0}</span>
              </div>
              <BarcodeStrip pct={heartbeatPct} tone="medium" />
            </div>
            <div>
              <div className="flex justify-between text-[12px] text-[#7A7A86] mb-1 tabular-nums">
                <span>Below Threshold</span>
                <span>{data?.below_threshold_count ?? 0}</span>
              </div>
              <BarcodeStrip pct={belowPct} tone="muted" />
            </div>
          </div>
        </Card>

        <Card className="p-5 space-y-3">
          <h2 className="text-[14px] font-semibold text-white">Top 5 Keyword Boost</h2>
          {(() => {
            const topBoosts = (data?.boost_keywords ?? [])
              .filter((kw) => kw.matches > 0)
              .sort((a, b) => b.matches - a.matches)
              .slice(0, 5)
            if (topBoosts.length === 0) {
              return (
                <p className="text-[12px] text-[#7A7A86]">
                  No keywords hit in the current window.
                </p>
              )
            }
            return (
              <div className="space-y-2">
                {topBoosts.map((kw) => (
                  <div
                    key={kw.keyword}
                    className="flex items-center justify-between px-3 py-2 rounded-xl bg-[#121215] border border-[#22222A] text-[12px]"
                  >
                    <span className="text-[#E4E4EA]">{kw.keyword}</span>
                    <span className="text-[#8B8B96] tabular-nums">{kw.matches} hits</span>
                  </div>
                ))}
              </div>
            )
          })()}
        </Card>

        <Card className="p-5 space-y-3">
          <h2 className="text-[14px] font-semibold text-white">Highest Publisher Tier</h2>
          <div className="grid grid-cols-2 gap-2">
            {[
              { id: 'all', label: 'All Tiers', count: data?.tier_counts?.all ?? 0 },
              { id: '1', label: 'Tier 1 Primary', count: data?.tier_counts?.['1'] ?? 0 },
              { id: '2', label: 'Tier 2 Wire', count: data?.tier_counts?.['2'] ?? 0 },
              { id: '3', label: 'Tier 3+ Niche', count: data?.tier_counts?.['3'] ?? 0 },
            ].map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => setTier(item.id)}
                className={cn(
                  'px-3 py-2.5 rounded-xl border text-left transition-colors cursor-pointer',
                  tier === item.id
                    ? 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D] border-[#E4E4EA]'
                    : 'bg-[#121215] border-[#24242C] text-[#A0A0AB] hover:text-white',
                )}
              >
                <div className="text-[11px] opacity-80">{item.label}</div>
                <div className="text-[15px] font-semibold tabular-nums mt-0.5">{item.count}</div>
              </button>
            ))}
          </div>
        </Card>
      </aside>
    </div>
  )
}
