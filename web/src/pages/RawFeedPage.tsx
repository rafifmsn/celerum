import { useCallback, useEffect, useState } from 'react'
import { ChevronLeft, ChevronRight, ExternalLink } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { fetchArticles } from '@/lib/api'
import { formatRelativeTime, tierLabel } from '@/lib/utils'
import type { ArticlesResponse } from '@/types/api'

interface RawFeedPageProps {
  searchQuery: string
  streamTick: number
}

export function RawFeedPage({ searchQuery, streamTick }: RawFeedPageProps) {
  const [category, setCategory] = useState('')
  const [source, setSource] = useState('')
  const [page, setPage] = useState(1)
  const [data, setData] = useState<ArticlesResponse | null>(null)
  const [loading, setLoading] = useState(true)

  const limit = 20

  const load = useCallback(async () => {
    try {
      const res = await fetchArticles({
        category,
        source,
        q: searchQuery,
        page,
        limit,
      })
      setData(res)
    } finally {
      setLoading(false)
    }
  }, [category, source, searchQuery, page])

  useEffect(() => {
    setPage(1)
  }, [category, source, searchQuery])

  useEffect(() => {
    void load()
  }, [load, streamTick])

  const totalCount = data?.total_count ?? 0
  const totalPages = Math.max(1, Math.ceil(totalCount / limit))
  const startIdx = totalCount === 0 ? 0 : (page - 1) * limit + 1
  const endIdx = Math.min(totalCount, page * limit)

  return (
    <div className="flex flex-col gap-5">
      <Card className="p-5 flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-[20px] font-semibold text-white">Raw Article Stream</h1>
          <p className="text-[12px] text-[#7A7A86] mt-0.5">
            Chronological RSS/Atom ingestion store
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-3">
          <Select
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            aria-label="Filter by category"
          >
            <option value="">All Categories</option>
            {(data?.categories ?? []).map((cat) => (
              <option key={cat} value={cat}>
                {cat}
              </option>
            ))}
          </Select>

          <Select
            value={source}
            onChange={(e) => setSource(e.target.value)}
            aria-label="Filter by source"
          >
            <option value="">All Sources</option>
            {(data?.sources ?? []).map((src) => (
              <option key={src} value={src}>
                {src}
              </option>
            ))}
          </Select>
        </div>
      </Card>

      {loading && !data ? (
        <Card className="p-8 text-center text-[13px] text-[#7A7A86]">Loading articles...</Card>
      ) : (data?.articles ?? []).length === 0 ? (
        <Card className="p-8 text-center text-[13px] text-[#7A7A86]">
          No articles ingested for the current filter criteria.
        </Card>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
          {(data?.articles ?? []).map((article) => {
            const desc = article.description?.trim()
            const hasDistinctDesc =
              Boolean(desc) && desc.toLowerCase() !== article.title.trim().toLowerCase()

            return (
              <a
                key={article.id}
                href={article.url}
                target="_blank"
                rel="noreferrer"
                className="bg-[#18181C] border border-[#22222A] hover:border-[#32323E] rounded-[22px] p-5 flex flex-col justify-between gap-3 transition-colors group"
              >
                <div className="space-y-2.5">
                  <div className="flex flex-wrap items-center gap-2 text-[11px]">
                    <Badge
                      variant={
                        article.feed_tier === 1
                          ? 'tier1'
                          : article.feed_tier === 2
                            ? 'tier2'
                            : 'tier3'
                      }
                    >
                      {tierLabel(article.feed_tier)} &middot;{' '}
                      {article.feed_name || article.feed_url}
                    </Badge>
                    {article.category && <Badge>{article.category}</Badge>}
                    <span className="text-[#7A7A86] tabular-nums">
                      {formatRelativeTime(article.published_at || article.ingested_at)}
                    </span>
                  </div>

                  <div className="flex items-start justify-between gap-3">
                    <h3 className="text-[15px] font-semibold text-white group-hover:text-white leading-snug">
                      {article.title}
                    </h3>
                    <ExternalLink className="w-3.5 h-3.5 text-[#6B6B76] opacity-0 group-hover:opacity-100 transition-opacity shrink-0 mt-1" />
                  </div>

                  {hasDistinctDesc && (
                    <p className="text-[12px] text-[#A0A0AB] line-clamp-2 leading-relaxed">
                      {desc}
                    </p>
                  )}
                </div>
              </a>
            )
          })}
        </div>
      )}

      <Card className="px-5 py-3.5 flex flex-wrap items-center justify-between gap-3 text-[12px] text-[#8B8B96]">
        <span className="tabular-nums">
          Showing {startIdx}-{endIdx} of {totalCount} articles
        </span>

        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={page <= 1}
            onClick={() => setPage((p) => Math.max(1, p - 1))}
          >
            <ChevronLeft className="w-3.5 h-3.5" />
            Prev
          </Button>
          <span className="tabular-nums px-2 text-[#D4D4DC]">
            Page {page} / {totalPages}
          </span>
          <Button
            size="sm"
            variant="outline"
            disabled={page >= totalPages}
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
          >
            Next
            <ChevronRight className="w-3.5 h-3.5" />
          </Button>
        </div>
      </Card>
    </div>
  )
}
