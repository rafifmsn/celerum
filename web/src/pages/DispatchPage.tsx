import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, ChevronLeft, ChevronRight, RefreshCw, Send, Trash2 } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  cancelDispatchWebhook,
  fetchDispatch,
  flushDispatch,
  retryDispatchWebhook,
} from '@/lib/api'
import { cn, formatCountdown, formatRelativeTime, tierLabel } from '@/lib/utils'
import type { DispatchResponse } from '@/types/api'

interface DispatchPageProps {
  searchQuery: string
  streamTick: number
}

export function DispatchPage({ searchQuery, streamTick }: DispatchPageProps) {
  const [data, setData] = useState<DispatchResponse | null>(null)
  const [tab, setTab] = useState<'pending' | 'history'>('pending')
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null)
  const [countdown, setCountdown] = useState(0)

  const limit = 20

  const load = useCallback(async () => {
    try {
      const res = await fetchDispatch(searchQuery, page, limit)
      setData(res)
      setCountdown(res.next_flush_in_seconds)
    } finally {
      setLoading(false)
    }
  }, [searchQuery, page])

  useEffect(() => {
    setPage(1)
  }, [searchQuery])

  useEffect(() => {
    void load()
  }, [load, streamTick])

  useEffect(() => {
    const id = window.setInterval(() => {
      setCountdown((prev) => (prev > 0 ? prev - 1 : 0))
    }, 1000)
    return () => window.clearInterval(id)
  }, [])

  const handleFlush = async () => {
    setBusy(true)
    setNotice(null)
    try {
      const res = await flushDispatch()
      setNotice({ kind: 'ok', text: `Flushed ${res.dispatched_count} buffered cluster(s).` })
      await load()
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Flush failed' })
    } finally {
      setBusy(false)
    }
  }

  const handleRetry = async (id: number) => {
    setBusy(true)
    setNotice(null)
    try {
      await retryDispatchWebhook(id)
      setNotice({ kind: 'ok', text: `Dispatched retry job #${id}.` })
      await load()
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Retry failed' })
    } finally {
      setBusy(false)
    }
  }

  const handleCancel = async (id: number) => {
    setBusy(true)
    setNotice(null)
    try {
      await cancelDispatchWebhook(id)
      setNotice({ kind: 'ok', text: `Cancelled retry job #${id}.` })
      await load()
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Cancel failed' })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <Card className="p-5 flex flex-wrap items-center justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-[20px] font-semibold text-white">Dispatch Queue & Retry Dead-Letter</h1>
          <p className="text-[12px] text-[#8B8B96] tabular-nums">
            Next Heartbeat Flush in {formatCountdown(countdown)}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-1 bg-[#121215] border border-[#24242C] rounded-full p-1">
            <button
              type="button"
              onClick={() => setTab('pending')}
              className={cn(
                'px-3.5 py-1.5 rounded-full text-[12px] font-medium transition-colors cursor-pointer',
                tab === 'pending' ? 'bg-white text-[#0B0B0D]' : 'text-[#8B8B96] hover:text-white',
              )}
            >
              Pending & Retrying
              <span className="hidden sm:inline tabular-nums">
                {' '}({(data?.retries.length ?? 0) + (data?.buffered.length ?? 0)})
              </span>
            </button>
            <button
              type="button"
              onClick={() => setTab('history')}
              className={cn(
                'px-3.5 py-1.5 rounded-full text-[12px] font-medium transition-colors cursor-pointer',
                tab === 'history' ? 'bg-white text-[#0B0B0D]' : 'text-[#8B8B96] hover:text-white',
              )}
            >
              Dispatched History
              <span className="hidden sm:inline tabular-nums">
                {' '}({data?.history_total ?? data?.history.length ?? 0})
              </span>
            </button>
          </div>

          <Button variant="primary" onClick={handleFlush} disabled={busy}>
            <Send className="w-3.5 h-3.5" />
            Flush Buffer Now
          </Button>
        </div>
      </Card>

      {notice && (
        <div
          className={`px-4 py-3 rounded-2xl border text-[13px] ${
            notice.kind === 'ok'
              ? 'bg-[#161B18] border-[#26382B] text-[#C5E8CF]'
              : 'bg-[#221618] border-[#3E2428] text-[#FCA5A5]'
          }`}
        >
          {notice.text}
        </div>
      )}

      {loading && !data ? (
        <Card className="p-8 text-center text-[13px] text-[#7A7A86]">Loading dispatch state...</Card>
      ) : tab === 'pending' ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-5 items-start">
          <Card className="p-5 space-y-4">
            <div className="flex items-center justify-between">
              <h2 className="text-[15px] font-semibold text-white">Active Delivery Retries</h2>
              <Badge>{data?.retries.length ?? 0} queued</Badge>
            </div>

            {(data?.retries ?? []).length === 0 ? (
              <div className="p-6 rounded-2xl bg-[#121215] border border-[#22222A] text-center text-[13px] text-[#7A7A86]">
                No webhook or Telegram deliveries awaiting retry.
              </div>
            ) : (
              <div className="space-y-3">
                {(data?.retries ?? []).map((retry) => (
                  <div
                    key={retry.id}
                    className="p-4 rounded-2xl bg-[#121215] border border-[#24242C] space-y-3"
                  >
                    <div className="flex items-center justify-between gap-2 text-[11px]">
                      <Badge>{retry.target_type}</Badge>
                      <span className="text-[#8B8B96] tabular-nums">
                        Attempt {retry.attempts} &middot; Next {formatRelativeTime(retry.next_retry_at)}
                      </span>
                    </div>

                    <div className="text-[13px] font-medium text-white">
                      {retry.headline || retry.target_url}
                    </div>

                    {retry.last_error && (
                      <div className="flex items-start gap-2 px-3 py-2 rounded-xl bg-[#221618] border border-[#3E2428] text-[12px] text-[#FCA5A5]">
                        <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
                        <span className="break-words">{retry.last_error}</span>
                      </div>
                    )}

                    <div className="flex items-center justify-end gap-2">
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => void handleCancel(retry.id)}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                        Cancel
                      </Button>
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={busy}
                        onClick={() => void handleRetry(retry.id)}
                      >
                        <RefreshCw className="w-3.5 h-3.5" />
                        Retry Immediately
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card className="p-5 space-y-4">
            <div className="flex items-center justify-between">
              <h2 className="text-[15px] font-semibold text-white">Buffered Unflushed Clusters</h2>
              <Badge>{data?.buffered.length ?? 0} buffered</Badge>
            </div>

            {(data?.buffered ?? []).length === 0 ? (
              <div className="p-6 rounded-2xl bg-[#121215] border border-[#22222A] text-center text-[13px] text-[#7A7A86]">
                Heartbeat buffer is currently empty.
              </div>
            ) : (
              <div className="space-y-3">
                {(data?.buffered ?? []).map((cluster) => (
                  <div
                    key={cluster.cluster_hash}
                    className="p-4 rounded-2xl bg-[#121215] border border-[#24242C] space-y-2"
                  >
                    <div className="flex items-center justify-between gap-2 text-[11px]">
                      <div className="flex items-center gap-2">
                        <Badge variant={cluster.tier === 1 ? 'tier1' : 'default'}>
                          {tierLabel(cluster.tier)} &middot; {cluster.lead_source}
                        </Badge>
                        <span className="text-[#8B8B96] tabular-nums">
                          {cluster.source_count} sources
                        </span>
                      </div>
                      <span className="font-semibold text-white tabular-nums">
                        {cluster.score.toFixed(2)}
                      </span>
                    </div>
                    <div className="text-[13px] font-medium text-[#F4F4F6]">{cluster.title}</div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <Card className="overflow-x-auto">
            {(data?.history ?? []).length === 0 ? (
              <div className="p-8 text-center text-[13px] text-[#7A7A86]">
                No clusters have been dispatched yet.
              </div>
            ) : (
              <table className="w-full text-left border-collapse min-w-[780px]">
                <thead>
                  <tr className="border-b border-[#22222A] text-[11px] uppercase tracking-wider text-[#7A7A86]">
                    <th className="py-3.5 px-5 font-medium">Headline</th>
                    <th className="py-3.5 px-4 font-medium">Lead Source</th>
                    <th className="py-3.5 px-4 font-medium">Mode</th>
                    <th className="py-3.5 px-4 font-medium">Targets</th>
                    <th className="py-3.5 px-4 font-medium">Score</th>
                    <th className="py-3.5 px-5 font-medium text-right">Dispatched</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#22222A] text-[13px]">
                  {(data?.history ?? []).map((row) => (
                    <tr key={row.cluster_hash} className="hover:bg-[#1D1D23] transition-colors">
                      <td className="py-3.5 px-5 font-medium text-white max-w-[360px] truncate">
                        {row.title}
                      </td>
                      <td className="py-3.5 px-4 text-[#A0A0AB]">
                        {tierLabel(row.tier)} &middot; {row.lead_source}
                      </td>
                      <td className="py-3.5 px-4">
                        <Badge variant={row.dispatch_mode === 'breaking' ? 'breaking' : 'heartbeat'}>
                          {row.dispatch_mode || 'dispatched'}
                        </Badge>
                      </td>
                      <td className="py-3.5 px-4 text-[#8B8B96]">{row.dispatch_targets || 'n/a'}</td>
                      <td className="py-3.5 px-4 font-semibold text-white tabular-nums">
                        {row.score.toFixed(2)}
                      </td>
                      <td className="py-3.5 px-5 text-right text-[#8B8B96] tabular-nums">
                        {formatRelativeTime(row.dispatched_at || row.updated_at)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>

          {(() => {
            const total = data?.history_total ?? 0
            const totalPages = Math.max(1, Math.ceil(total / limit))
            const startIdx = total === 0 ? 0 : (page - 1) * limit + 1
            const endIdx = Math.min(total, page * limit)
            return (
              <Card className="px-5 py-3.5 flex flex-wrap items-center justify-between gap-3 text-[12px] text-[#8B8B96]">
                <span className="tabular-nums">
                  Showing {startIdx}-{endIdx} of {total} dispatched
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
            )
          })()}
        </div>
      )}
    </div>
  )
}
