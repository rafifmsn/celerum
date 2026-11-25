import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ArrowUpRight,
  Clock,
  ExternalLink,
  Filter,
  Flame,
  Layers,
  Plus,
  Send,
  Sparkles,
  Zap,
} from 'lucide-react'
import { BarcodeStrip, SemiCircularGauge, WaveCombChart } from '@/components/Charts'
import { FormattedSummary } from '@/components/FormattedSummary'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { flushDispatch } from '@/lib/api'
import { cn, formatCountdown, formatRelativeTime, formatUtcTime, tierLabel } from '@/lib/utils'
import type { OverviewClusterSummary, OverviewMetrics } from '@/types/api'

interface OverviewPageProps {
  data: OverviewMetrics | null
  tier: string
  onTierChange: (next: string) => void
  searchQuery: string
  onRefresh: () => void
}

const TIER_PILLS = [
  { id: 'all', label: 'All Tiers' },
  { id: '1', label: 'Tier 1' },
  { id: '2', label: 'Tier 2' },
  { id: '3', label: 'Tier 3+' },
]

export function OverviewPage({
  data,
  tier,
  onTierChange,
  searchQuery,
  onRefresh,
}: OverviewPageProps) {
  const navigate = useNavigate()
  const [selectedClusterId, setSelectedClusterId] = useState<string | null>(null)
  const [summaryExpanded, setSummaryExpanded] = useState(false)
  const [rightFilter, setRightFilter] = useState('')
  const [flushing, setFlushing] = useState(false)
  const [flushNotice, setFlushNotice] = useState<string | null>(null)
  const [countdown, setCountdown] = useState(data?.next_flush_in_seconds ?? 0)

  useEffect(() => {
    setCountdown(data?.next_flush_in_seconds ?? 0)
  }, [data?.next_flush_in_seconds])

  useEffect(() => {
    const id = window.setInterval(() => {
      setCountdown((prev) => (prev > 0 ? prev - 1 : 0))
    }, 1000)
    return () => window.clearInterval(id)
  }, [])

  const clusters = useMemo(() => {
    const list = data?.top_clusters ?? []
    const q = (rightFilter || searchQuery).trim().toLowerCase()
    if (!q) return list
    return list.filter(
      (c) =>
        c.headline.toLowerCase().includes(q) ||
        c.lead_source.toLowerCase().includes(q) ||
        c.category.toLowerCase().includes(q) ||
        c.summary.toLowerCase().includes(q),
    )
  }, [data?.top_clusters, rightFilter, searchQuery])

  const activeCluster: OverviewClusterSummary | null = useMemo(() => {
    if (clusters.length === 0) return null
    return clusters.find((c) => c.id === selectedClusterId) ?? clusters[0]
  }, [clusters, selectedClusterId])

  useEffect(() => {
    setSummaryExpanded(false)
  }, [activeCluster?.id])

  const rawSummary =
    activeCluster?.summary ||
    'No synthesized paragraph recorded yet; inspect linked publisher coverage below.'

  const handleFlush = async () => {
    setFlushing(true)
    setFlushNotice(null)
    try {
      const res = await flushDispatch()
      setFlushNotice(`Flushed ${res.dispatched_count} buffered cluster(s)`)
      onRefresh()
    } catch (err) {
      setFlushNotice(err instanceof Error ? err.message : 'Flush failed')
    } finally {
      setFlushing(false)
    }
  }

  const fanCards = [
    clusters[0] ?? null,
    clusters[1] ?? null,
    clusters[2] ?? null,
  ]

  return (
    <div className="grid grid-cols-1 xl:grid-cols-12 gap-5 items-start">
      <div className="xl:col-span-8 flex flex-col gap-5">
        <Card className="p-6 sm:p-7 relative overflow-hidden">
          <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-center">
            <div className="lg:col-span-6 flex flex-col gap-4">
              <div>
                <h1 className="text-[28px] sm:text-[32px] font-semibold tracking-tight text-white leading-tight">
                  Command Center
                </h1>
                <p className="text-[13px] text-[#8B8B96] mt-1.5 max-w-72">
                  Real-time ingestion velocity, shingle clustering compression, and SLA webhook delivery.
                </p>
              </div>

              <div className="flex flex-wrap items-center gap-2 pt-1">
                <div className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-[#121215] border border-[#24242C] text-[12px] text-[#A0A0AB] tabular-nums">
                  <Clock className="w-3.5 h-3.5 text-[#7A7A86]" />
                  <span>
                    Window: {formatUtcTime(data?.window_start_utc)} -{' '}
                    {formatUtcTime(data?.window_end_utc)}
                  </span>
                </div>

                <div className="flex items-center gap-1 bg-[#121215] border border-[#24242C] rounded-full p-1">
                  {TIER_PILLS.map((pill) => (
                    <button
                      key={pill.id}
                      type="button"
                      onClick={() => onTierChange(pill.id)}
                      className={cn(
                        'px-2.5 py-1 rounded-full text-[11px] font-medium transition-colors cursor-pointer',
                        tier === pill.id
                          ? 'bg-gradient-to-b from-[#F4F4F6] to-[#D4D4DC] text-[#0B0B0D]'
                          : 'text-[#8B8B96] hover:text-white',
                      )}
                    >
                      {pill.label}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <div className="lg:col-span-6 relative h-[180px] hidden sm:flex items-center justify-center">
              {fanCards.map((card, idx) => {
                const layouts = [
                  ' -rotate-6 -translate-x-12 translate-y-2 z-10 bg-[#141418] opacity-75',
                  ' rotate-0 translate-y-0 z-20 bg-[#1B1B21] shadow-2xl',
                  ' rotate-6 translate-x-12 translate-y-3 z-10 bg-[#131317] opacity-75',
                ]
                return (
                  <button
                    key={card?.id ?? `empty-${idx}`}
                    type="button"
                    onClick={() => card && setSelectedClusterId(card.id)}
                    className={cn(
                      'absolute w-[220px] rounded-[20px] border border-[#2A2A34] p-4 text-left transition-transform hover:scale-[1.03] cursor-pointer',
                      layouts[idx],
                    )}
                  >
                    <div className="flex items-center justify-between gap-2 text-[11px] text-[#8B8B96] mb-2">
                      <span className="truncate">{card?.lead_source || 'Standby Feed'}</span>
                      <span className="px-1.5 py-0.5 rounded bg-[#24242E] text-white font-semibold tabular-nums">
                        {card ? card.score.toFixed(1) : '0.0'}
                      </span>
                    </div>
                    <p className="text-[12px] font-medium text-[#F4F4F6] line-clamp-3 leading-snug">
                      {card?.headline || 'Waiting for clustered intelligence articles in window'}
                    </p>
                    <div className="flex items-center justify-between text-[10px] text-[#6B6B76] mt-3 tabular-nums">
                      <span>{card ? tierLabel(card.tier) : 'T3'}</span>
                      <span>{card ? `${card.source_count} sources` : '0 sources'}</span>
                    </div>
                  </button>
                )
              })}
            </div>
          </div>
        </Card>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
          <Card className="p-5 flex flex-col justify-between gap-4">
            <div className="flex items-center justify-between">
              <span className="text-[13px] font-medium text-[#9A9AA6]">Cluster Compression</span>
              <Layers className="w-4 h-4 text-[#6B6B76]" />
            </div>

            <div className="flex items-baseline justify-between gap-2">
              <div className="text-[32px] font-semibold tracking-tight text-white tabular-nums">
                {Math.round(data?.dedup_efficiency_pct ?? 0)}%
              </div>
              <div className="text-[12px] text-[#8B8B96] tabular-nums">
                {data?.shingled_count ?? 0} / {data?.raw_fetched_count ?? 0}
              </div>
            </div>

            <div className="space-y-2.5">
              <div>
                <div className="flex justify-between text-[11px] text-[#7A7A86] mb-1 tabular-nums">
                  <span>Unique Clusters</span>
                  <span>{data?.window_clusters ?? 0}</span>
                </div>
                <BarcodeStrip pct={data?.dedup_efficiency_pct ?? 0} tone="bright" />
              </div>
              <div>
                <div className="flex justify-between text-[11px] text-[#7A7A86] mb-1 tabular-nums">
                  <span>Index Pruned Pairs</span>
                  <span>{Math.round(data?.pruned_pair_ratio ?? 0)}%</span>
                </div>
                <BarcodeStrip pct={data?.pruned_pair_ratio ?? 0} tone="muted" />
              </div>
            </div>
          </Card>

          <Card className="p-5 flex flex-col justify-between gap-2">
            <div className="flex items-center justify-between">
              <span className="text-[13px] font-medium text-[#9A9AA6]">Conditional 304 Rate</span>
              <Zap className="w-4 h-4 text-[#6B6B76]" />
            </div>

            <SemiCircularGauge
              value={data?.cache_hit_pct ?? 0}
              label={`${data?.feeds_304_count ?? 0} of ${data?.feeds_total_active ?? 0} cached`}
            />

            <div className="flex items-center justify-between text-[11px] text-[#7A7A86] pt-1 border-t border-[#22222A] tabular-nums">
              <span>Bandwidth Saved Ratio</span>
              <span className="text-[#F4F4F6] font-medium">
                {Math.round(data?.conditional_304_ratio ?? 0)}%
              </span>
            </div>
          </Card>

          <Card className="p-5 flex flex-col justify-between gap-4">
            <div className="flex items-center justify-between">
              <span className="text-[13px] font-medium text-[#9A9AA6]">Breaking Signal Rate</span>
              <Flame className="w-4 h-4 text-[#6B6B76]" />
            </div>

            <div className="flex items-baseline justify-between">
              <div className="text-[32px] font-semibold tracking-tight text-white tabular-nums">
                {Math.round(data?.breaking_rate_pct ?? 0)}%
              </div>
              <Badge variant="breaking" className="tabular-nums">
                &ge; {(data?.breaking_threshold ?? 8).toFixed(1)}
              </Badge>
            </div>

            <div className="space-y-2.5">
              <div>
                <div className="flex justify-between text-[11px] text-[#7A7A86] mb-1 tabular-nums">
                  <span>Breaking Clusters</span>
                  <span>{data?.breaking_clusters_count ?? 0}</span>
                </div>
                <BarcodeStrip pct={data?.breaking_rate_pct ?? 0} tone="bright" />
              </div>
              <div>
                <div className="flex justify-between text-[11px] text-[#7A7A86] mb-1 tabular-nums">
                  <span>Window Raw Articles</span>
                  <span>{data?.window_raw_articles ?? 0}</span>
                </div>
                <BarcodeStrip
                  pct={Math.min(100, ((data?.active_clusters_count ?? 0) / Math.max(1, data?.window_raw_articles ?? 1)) * 100)}
                  tone="medium"
                />
              </div>
            </div>
          </Card>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
          <Card className="p-5 flex flex-col gap-4">
            <div className="flex items-center justify-between gap-3">
              <h2 className="text-[14px] font-semibold text-white">Pipeline Efficiency Matrix</h2>
              <Badge>{Math.round(data?.llm_enriched_pct ?? 0)}% LLM</Badge>
            </div>

            <WaveCombChart
              series={data?.hourly_efficiency_series ?? []}
              rightBaselineLabel={formatUtcTime(data?.window_end_utc).replace(' UTC', '')}
            />

            <div className="grid grid-cols-4 gap-2 pt-2 border-t border-[#22222A] text-center tabular-nums">
              <div>
                <div className="text-[11px] text-[#6B6B76]">Shingle Prune</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {Math.round(data?.index_pruned_pct ?? 0)}%
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[#6B6B76]">Similarity Cut</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {Math.round(data?.jaccard_reduction_pct ?? 0)}%
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[#6B6B76]">Scraper OK</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {Math.round(data?.scraper_success_pct ?? 0)}%
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[#6B6B76]">RSS Fallback</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {Math.round(data?.rss_fallback_pct ?? 0)}%
                </div>
              </div>
            </div>
          </Card>

          <Card className="p-5 flex flex-col gap-4">
            <div className="flex items-center justify-between gap-3">
              <h2 className="text-[14px] font-semibold text-white">Dispatch & SLA Matrix</h2>
              <Badge>{Math.round(data?.delivery_sla_pct ?? 100)}% SLA</Badge>
            </div>

            <WaveCombChart
              series={data?.hourly_dispatch_series ?? []}
              rightBaselineLabel={formatCountdown(countdown)}
            />

            <div className="grid grid-cols-4 gap-2 pt-2 border-t border-[#22222A] text-center tabular-nums">
              <div>
                <div className="text-[11px] text-[#6B6B76]">Dispatched</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {data?.dispatched_total ?? 0}
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[#6B6B76]">Immediate</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {Math.round(data?.immediate_alert_pct ?? 0)}%
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[#6B6B76]">Retry Queue</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {data?.retry_queue_count ?? 0}
                </div>
              </div>
              <div>
                <div className="text-[11px] text-[#6B6B76]">Buffered</div>
                <div className="text-[14px] font-semibold text-white mt-0.5">
                  {data?.top_k_buffered_count ?? 0}
                </div>
              </div>
            </div>
          </Card>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {clusters.slice(0, 3).map((cluster) => {
            const selected = activeCluster?.id === cluster.id
            return (
              <button
                key={cluster.id}
                type="button"
                onClick={() => setSelectedClusterId(cluster.id)}
                className={cn(
                  'text-left bg-[#18181C] border rounded-[20px] p-4 transition-colors cursor-pointer flex flex-col justify-between gap-3',
                  selected
                    ? 'border-white/70 bg-[#1D1D24]'
                    : 'border-[#22222A] hover:border-[#363644]',
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <Badge variant={cluster.tier === 1 ? 'tier1' : 'default'}>
                    {tierLabel(cluster.tier)} &middot; {cluster.lead_source}
                  </Badge>
                  <span className="text-[12px] font-semibold text-white tabular-nums">
                    {cluster.score.toFixed(1)}
                  </span>
                </div>
                <h3 className="text-[13px] font-medium text-[#F4F4F6] line-clamp-2 leading-snug">
                  {cluster.headline}
                </h3>
                <div className="flex items-center justify-between text-[11px] text-[#7A7A86] tabular-nums">
                  <span>{cluster.source_count} sources</span>
                  <span>{formatRelativeTime(cluster.updated_at)}</span>
                </div>
              </button>
            )
          })}
        </div>
      </div>

      <aside className="xl:col-span-4 bg-[#141418] border border-[#22222A] rounded-[24px] p-5 flex flex-col gap-4 xl:sticky xl:top-6">
        <div className="flex items-center gap-2">
          <Sparkles className="w-4 h-4 text-white" />
          <h2 className="text-[14px] font-semibold text-white">AI Synthesis Briefing</h2>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => navigate('/sources')}>
            Sources
            <ArrowUpRight className="w-3.5 h-3.5" />
          </Button>
          <Button size="sm" variant="outline" onClick={() => navigate('/raw-feed')}>
            Raw RSS
            <ArrowUpRight className="w-3.5 h-3.5" />
          </Button>
          <Button size="sm" variant="primary" onClick={handleFlush} disabled={flushing}>
            <Send className="w-3.5 h-3.5" />
            {flushing ? 'Flushing...' : 'Flush Now'}
          </Button>
        </div>

        {flushNotice && (
          <div className="px-3 py-2 rounded-xl bg-[#1D1D24] border border-[#2C2C38] text-[12px] text-[#D4D4DC]">
            {flushNotice}
          </div>
        )}

        {activeCluster ? (
          <div className="bg-[#18181C] border border-[#24242C] rounded-[18px] p-4 flex flex-col gap-3">
            <div className="flex items-center justify-between gap-2 text-[11px]">
              <Badge variant={activeCluster.score >= (data?.breaking_threshold ?? 8) ? 'breaking' : 'heartbeat'}>
                Score {activeCluster.score.toFixed(2)}
              </Badge>
              <span className="text-[#7A7A86] tabular-nums">
                {formatRelativeTime(activeCluster.updated_at)}
              </span>
            </div>

            <h3 className="text-[15px] font-semibold text-white leading-snug">
              {activeCluster.headline}
            </h3>

            <FormattedSummary
              text={rawSummary}
              maxChars={220}
              expanded={summaryExpanded}
              onToggleExpand={() => setSummaryExpanded((prev) => !prev)}
            />

            <div className="border-t border-[#24242C] pt-3 space-y-2">
              <div className="text-[11px] uppercase tracking-wider text-[#6B6B76]">
                Corroborating Sources ({activeCluster.sources?.length ?? activeCluster.source_count})
              </div>
              {(activeCluster.sources ?? []).slice(0, 4).map((src, idx) => (
                <a
                  key={`${src.url}-${idx}`}
                  href={src.url}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center justify-between gap-2 px-2.5 py-2 rounded-xl bg-[#121215] hover:bg-[#1E1E24] transition-colors text-[12px]"
                >
                  <span className="truncate text-[#D4D4DC]">
                    {tierLabel(src.tier)} &middot; {src.name}
                  </span>
                  <ExternalLink className="w-3.5 h-3.5 shrink-0 text-[#7A7A86]" />
                </a>
              ))}
            </div>
          </div>
        ) : (
          <div className="bg-[#18181C] border border-[#24242C] rounded-[18px] p-6 text-center text-[13px] text-[#7A7A86]">
            No clusters match the current tier or filter criteria.
          </div>
        )}

        <div className="space-y-1.5 max-h-[210px] overflow-y-auto pr-1">
          {clusters.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setSelectedClusterId(item.id)}
              className={cn(
                'w-full text-left px-3 py-2.5 rounded-xl border transition-colors cursor-pointer flex items-center justify-between gap-2',
                activeCluster?.id === item.id
                  ? 'bg-[#202028] border-[#383846] text-white'
                  : 'bg-[#121215] border-[#1F1F26] text-[#9A9AA6] hover:text-white',
              )}
            >
              <span className="truncate text-[12px] font-medium">{item.headline}</span>
              <span className="text-[11px] tabular-nums text-[#7A7A86] shrink-0">
                {item.score.toFixed(1)}
              </span>
            </button>
          ))}
        </div>

        <div className="mt-auto pt-2 border-t border-[#22222A]">
          <div className="flex items-center gap-2 bg-[#121215] border border-[#24242C] rounded-2xl p-2">
            <button
              type="button"
              disabled
              title="Interactive AI query coming soon"
              aria-label="Add context (Interactive AI query coming soon)"
              className="w-8 h-8 rounded-xl bg-[#18181C] text-[#6B6B76] flex items-center justify-center opacity-50 cursor-not-allowed shrink-0"
            >
              <Plus className="w-4 h-4" />
            </button>

            <div className="relative flex-1">
              <Filter className="w-3.5 h-3.5 text-[#6B6B76] absolute left-2 top-1/2 -translate-y-1/2 pointer-events-none" />
              <input
                type="text"
                value={rightFilter}
                onChange={(e) => setRightFilter(e.target.value)}
                placeholder="Filter visible clusters..."
                className="w-full bg-transparent pl-7 pr-2 py-1 text-[12px] text-[#F4F4F6] placeholder-[#6B6B76] focus:outline-none"
              />
            </div>

            <button
              type="button"
              disabled
              title="Interactive AI query coming soon"
              aria-label="Send AI prompt (Interactive AI query coming soon)"
              className="w-8 h-8 rounded-xl bg-[#18181C] text-[#6B6B76] flex items-center justify-center opacity-50 cursor-not-allowed shrink-0"
            >
              <Send className="w-3.5 h-3.5" />
            </button>
          </div>
          <p className="text-[11px] text-[#6B6B76] mt-1.5 px-1">
            Client-side cluster filter active. Interactive AI query coming soon.
          </p>
        </div>
      </aside>
    </div>
  )
}
