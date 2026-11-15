import { useCallback, useEffect, useState } from 'react'
import { ChevronLeft, ChevronRight, ExternalLink, Image as ImageIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
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
  const [showThumbnails, setShowThumbnails] = useState(true)
  const [brokenImages, setBrokenImages] = useState<Record<string, boolean>>({})
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
          <p className="text-[12px] text-[#7A7A86] mt-0.5 tabular-nums">
            Chronological RSS/Atom ingestion store &middot; {totalCount} persisted article(s)
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

          <div className="flex items-center gap-2.5 px-3 py-1.5 rounded-xl bg-[#121215] border border-[#24242C]">
            <ImageIcon className="w-3.5 h-3.5 text-[#8B8B96]" />
            <span className="text-[12px] text-[#D4D4DC]">Show Thumbnails</span>
            <Switch
              checked={showThumbnails}
              onCheckedChange={setShowThumbnails}
              label="Show Thumbnails"
            />
          </div>
        </div>
      </Card>

      <Card className="overflow-hidden">
        {loading && !data ? (
          <div className="p-8 text-center text-[13px] text-[#7A7A86]">Loading articles...</div>
        ) : (data?.articles ?? []).length === 0 ? (
          <div className="p-8 text-center text-[13px] text-[#7A7A86]">
            No articles ingested for the current filter criteria.
          </div>
        ) : (
          <div className="divide-y divide-[#22222A]">
            {(data?.articles ?? []).map((article) => {
              const desc = article.description?.trim()
              const hasDistinctDesc =
                Boolean(desc) && desc.toLowerCase() !== article.title.trim().toLowerCase()
              const canShowThumb =
                showThumbnails && Boolean(article.image_url) && !brokenImages[article.id]

              return (
                <a
                  key={article.id}
                  href={article.url}
                  target="_blank"
                  rel="noreferrer"
                  className="px-5 py-4 flex items-center justify-between gap-4 hover:bg-[#1D1D23] transition-colors group"
                >
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2 text-[11px] text-[#8B8B96]">
                      <Badge variant={article.feed_tier === 1 ? 'tier1' : 'default'}>
                        {tierLabel(article.feed_tier)}
                      </Badge>
                      <span className="font-medium text-[#D4D4DC]">
                        {article.feed_name || article.feed_url}
                      </span>
                      {article.category && (
                        <span className="px-2 py-0.5 rounded bg-[#121215] border border-[#24242C]">
                          {article.category}
                        </span>
                      )}
                      <span className="tabular-nums">
                        &middot; {formatRelativeTime(article.published_at || article.ingested_at)}
                      </span>
                    </div>

                    <div className="flex items-center gap-2">
                      <h3 className="text-[14px] font-medium text-white group-hover:text-white leading-snug">
                        {article.title}
                      </h3>
                      <ExternalLink className="w-3.5 h-3.5 text-[#6B6B76] opacity-0 group-hover:opacity-100 transition-opacity shrink-0" />
                    </div>

                    {hasDistinctDesc && (
                      <p className="text-[12px] text-[#8B8B96] line-clamp-2">{desc}</p>
                    )}
                  </div>

                  {canShowThumb && (
                    <img
                      src={article.image_url}
                      alt=""
                      loading="lazy"
                      onError={() =>
                        setBrokenImages((prev) => ({ ...prev, [article.id]: true }))
                      }
                      className="w-20 h-14 object-cover rounded-xl border border-[#262630] shrink-0 bg-[#121215]"
                    />
                  )}
                </a>
              )
            })}
          </div>
        )}

        <div className="px-5 py-3.5 bg-[#141418] border-t border-[#22222A] flex flex-wrap items-center justify-between gap-3 text-[12px] text-[#8B8B96]">
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
        </div>
      </Card>
    </div>
  )
}
