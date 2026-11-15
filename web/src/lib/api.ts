import type {
  ArticlesResponse,
  ClustersResponse,
  DispatchResponse,
  DoctorReport,
  OverviewMetrics,
  SettingsPayload,
  SourceRecord,
  SourcesResponse,
} from '@/types/api'

export class ApiError extends Error {
  status: number
  field?: string

  constructor(message: string, status: number, field?: string) {
    super(message)
    this.status = status
    this.field = field
  }
}

async function requestJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    ...init,
  })
  const text = await res.text()
  const payload = text ? JSON.parse(text) : {}
  if (!res.ok) {
    throw new ApiError(
      payload?.error || `Request failed with HTTP ${res.status}`,
      res.status,
      payload?.field,
    )
  }
  return payload as T
}

export function fetchOverview(tier = 'all'): Promise<OverviewMetrics> {
  const params = new URLSearchParams({ tier })
  return requestJson<OverviewMetrics>(`/api/overview?${params.toString()}`)
}

export function fetchClusters(opts: {
  filter?: string
  tier?: string
  sort?: string
  q?: string
}): Promise<ClustersResponse> {
  const params = new URLSearchParams()
  if (opts.filter) params.set('filter', opts.filter)
  if (opts.tier) params.set('tier', opts.tier)
  if (opts.sort) params.set('sort', opts.sort)
  if (opts.q) params.set('q', opts.q)
  return requestJson<ClustersResponse>(`/api/clusters?${params.toString()}`)
}

export function fetchArticles(opts: {
  category?: string
  source?: string
  q?: string
  page?: number
  limit?: number
}): Promise<ArticlesResponse> {
  const params = new URLSearchParams()
  if (opts.category) params.set('category', opts.category)
  if (opts.source) params.set('source', opts.source)
  if (opts.q) params.set('q', opts.q)
  params.set('page', String(opts.page ?? 1))
  params.set('limit', String(opts.limit ?? 20))
  return requestJson<ArticlesResponse>(`/api/articles?${params.toString()}`)
}

export function fetchSources(q = ''): Promise<SourcesResponse> {
  const params = new URLSearchParams()
  if (q) params.set('q', q)
  return requestJson<SourcesResponse>(`/api/sources?${params.toString()}`)
}

export function createSource(source: Partial<SourceRecord>): Promise<SourceRecord> {
  return requestJson<SourceRecord>('/api/sources', {
    method: 'POST',
    body: JSON.stringify(source),
  })
}

export function updateSource(id: string, source: Partial<SourceRecord>): Promise<SourceRecord> {
  return requestJson<SourceRecord>(`/api/sources/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(source),
  })
}

export function deleteSource(id: string): Promise<{ deleted: boolean }> {
  return requestJson<{ deleted: boolean }>(`/api/sources/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
}

export async function exportConfigYaml(): Promise<string> {
  const res = await fetch('/api/config/yaml')
  if (!res.ok) {
    throw new ApiError(`Failed to export YAML (HTTP ${res.status})`, res.status)
  }
  return res.text()
}

export function importConfigYaml(yaml: string): Promise<{ imported: boolean; sources: number }> {
  return requestJson<{ imported: boolean; sources: number }>('/api/config/yaml', {
    method: 'POST',
    body: JSON.stringify({ yaml }),
  })
}

export function fetchDispatch(q = '', page = 1, limit = 20): Promise<DispatchResponse> {
  const params = new URLSearchParams()
  if (q) params.set('q', q)
  params.set('page', String(page))
  params.set('limit', String(limit))
  return requestJson<DispatchResponse>(`/api/dispatch?${params.toString()}`)
}

export function flushDispatch(): Promise<{ flushed: boolean; dispatched_count: number }> {
  return requestJson<{ flushed: boolean; dispatched_count: number }>('/api/dispatch/flush', {
    method: 'POST',
  })
}

export function retryDispatchWebhook(id: number): Promise<{ status: string }> {
  return requestJson<{ status: string }>(`/api/dispatch/retries/${id}/retry`, {
    method: 'POST',
  })
}

export function cancelDispatchWebhook(id: number): Promise<{ deleted: boolean }> {
  return requestJson<{ deleted: boolean }>(`/api/dispatch/retries/${id}`, {
    method: 'DELETE',
  })
}

export function fetchSettings(): Promise<SettingsPayload> {
  return requestJson<SettingsPayload>('/api/settings')
}

export function saveSettings(payload: SettingsPayload): Promise<{ saved: boolean }> {
  return requestJson<{ saved: boolean }>('/api/settings', {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export function discoverModels(body: {
  provider: string
  base_url: string
  api_key: string
}): Promise<{ models: string[]; endpoint: string }> {
  return requestJson<{ models: string[]; endpoint: string }>('/api/models/discover', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export function runDoctor(send = false, settings?: SettingsPayload): Promise<DoctorReport> {
  return requestJson<DoctorReport>('/api/doctor', {
    method: 'POST',
    body: JSON.stringify(settings ? { send, settings } : { send }),
  })
}
