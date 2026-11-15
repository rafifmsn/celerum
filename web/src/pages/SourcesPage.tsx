import { useCallback, useEffect, useRef, useState, type ChangeEvent, type FormEvent } from 'react'
import { Download, Edit3, Plus, Trash2, Upload } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  createSource,
  deleteSource,
  exportConfigYaml,
  fetchSources,
  importConfigYaml,
  updateSource,
} from '@/lib/api'
import { formatRelativeTime, tierLabel } from '@/lib/utils'
import type { SourceRecord, SourcesResponse } from '@/types/api'

interface SourcesPageProps {
  searchQuery: string
  streamTick: number
}

interface SourceFormState {
  id: string
  name: string
  url: string
  category: string
  tier: number
  enabled: boolean
}

const EMPTY_FORM: SourceFormState = {
  id: '',
  name: '',
  url: '',
  category: 'General',
  tier: 2,
  enabled: true,
}

export function SourcesPage({ searchQuery, streamTick }: SourcesPageProps) {
  const [data, setData] = useState<SourcesResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [notice, setNotice] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null)
  const [modalMode, setModalMode] = useState<'add' | 'edit' | null>(null)
  const [form, setForm] = useState<SourceFormState>(EMPTY_FORM)
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<SourceRecord | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await fetchSources(searchQuery)
      setData(res)
    } finally {
      setLoading(false)
    }
  }, [searchQuery])

  useEffect(() => {
    void load()
  }, [load, streamTick])

  const openAdd = () => {
    setForm(EMPTY_FORM)
    setModalMode('add')
  }

  const openEdit = (src: SourceRecord) => {
    setForm({
      id: src.id,
      name: src.name,
      url: src.url,
      category: src.category || 'General',
      tier: src.tier || 2,
      enabled: src.enabled,
    })
    setModalMode('edit')
  }

  const handleSave = async (e: FormEvent) => {
    e.preventDefault()
    if (!form.name.trim() || !form.url.trim()) {
      setNotice({ kind: 'err', text: 'Name and Feed URL are required.' })
      return
    }
    setSaving(true)
    setNotice(null)
    try {
      if (modalMode === 'add') {
        await createSource({
          name: form.name.trim(),
          url: form.url.trim(),
          category: form.category.trim() || 'General',
          tier: form.tier,
          enabled: form.enabled,
        })
        setNotice({ kind: 'ok', text: `Added feed "${form.name.trim()}"` })
      } else if (modalMode === 'edit') {
        await updateSource(form.id, {
          name: form.name.trim(),
          url: form.url.trim(),
          category: form.category.trim() || 'General',
          tier: form.tier,
          enabled: form.enabled,
        })
        setNotice({ kind: 'ok', text: `Updated feed "${form.name.trim()}"` })
      }
      setModalMode(null)
      await load()
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Save failed' })
    } finally {
      setSaving(false)
    }
  }

  const handleToggleEnabled = async (src: SourceRecord, next: boolean) => {
    setNotice(null)
    try {
      await updateSource(src.id, {
        name: src.name,
        url: src.url,
        category: src.category,
        tier: src.tier,
        enabled: next,
      })
      await load()
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Toggle failed' })
    }
  }

  const handleConfirmDelete = async () => {
    if (!deleteTarget) return
    setSaving(true)
    setNotice(null)
    try {
      await deleteSource(deleteTarget.id)
      setNotice({ kind: 'ok', text: `Deleted feed "${deleteTarget.name}"` })
      setDeleteTarget(null)
      await load()
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Delete failed' })
    } finally {
      setSaving(false)
    }
  }

  const handleExportYaml = async () => {
    setNotice(null)
    try {
      const yaml = await exportConfigYaml()
      const blob = new Blob([yaml], { type: 'application/x-yaml' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'celerum.yaml'
      a.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Export failed' })
    }
  }

  const handleImportYaml = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    setNotice(null)
    try {
      const text = await file.text()
      const res = await importConfigYaml(text)
      setNotice({ kind: 'ok', text: `Imported YAML config (${res.sources} feed sources synced)` })
      await load()
    } catch (err) {
      setNotice({ kind: 'err', text: err instanceof Error ? err.message : 'Import failed' })
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <Card className="p-5 flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-[20px] font-semibold text-white">Feed Sources & Polling Health</h1>
          <p className="text-[12px] text-[#7A7A86] mt-0.5 tabular-nums">
            Adaptive ETag/Last-Modified scheduling &middot; {data?.etag_cached_count ?? 0} cached /{' '}
            {data?.updated_200_count ?? 0} fresh
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2.5">
          <input
            ref={fileInputRef}
            type="file"
            accept=".yaml,.yml,text/yaml"
            className="hidden"
            onChange={handleImportYaml}
          />
          <Button variant="outline" onClick={() => fileInputRef.current?.click()}>
            <Upload className="w-3.5 h-3.5" />
            Import YAML
          </Button>
          <Button variant="outline" onClick={handleExportYaml}>
            <Download className="w-3.5 h-3.5" />
            Export YAML
          </Button>
          <Button variant="primary" onClick={openAdd}>
            <Plus className="w-3.5 h-3.5" />
            Add Source
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

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <Card className="p-5 flex items-center justify-between">
          <div>
            <div className="text-[12px] text-[#8B8B96]">Tier 1 Primary Sources (3.0x weight)</div>
            <div className="text-[26px] font-semibold text-white tabular-nums mt-1">
              {data?.tier1_count ?? 0}
            </div>
          </div>
          <Badge variant="tier1">T1</Badge>
        </Card>
        <Card className="p-5 flex items-center justify-between">
          <div>
            <div className="text-[12px] text-[#8B8B96]">Tier 2 Wire Services (2.0x weight)</div>
            <div className="text-[26px] font-semibold text-white tabular-nums mt-1">
              {data?.tier2_count ?? 0}
            </div>
          </div>
          <Badge variant="tier2">T2</Badge>
        </Card>
        <Card className="p-5 flex items-center justify-between">
          <div>
            <div className="text-[12px] text-[#8B8B96]">Tier 3+ Niche Feeds (1.0x weight)</div>
            <div className="text-[26px] font-semibold text-white tabular-nums mt-1">
              {data?.tier3_count ?? 0}
            </div>
          </div>
          <Badge variant="tier3">T3+</Badge>
        </Card>
      </div>

      <Card className="overflow-x-auto">
        {loading && !data ? (
          <div className="p-8 text-center text-[13px] text-[#7A7A86]">Loading sources...</div>
        ) : (data?.sources ?? []).length === 0 ? (
          <div className="p-8 text-center text-[13px] text-[#7A7A86]">
            No feed sources configured yet. Click &ldquo;Add Source&rdquo; or import a YAML file.
          </div>
        ) : (
          <table className="w-full text-left border-collapse min-w-[820px]">
            <thead>
              <tr className="border-b border-[#22222A] text-[11px] uppercase tracking-wider text-[#7A7A86]">
                <th className="py-3.5 px-5 font-medium">Name</th>
                <th className="py-3.5 px-4 font-medium">Feed URL</th>
                <th className="py-3.5 px-4 font-medium">Category</th>
                <th className="py-3.5 px-4 font-medium">Tier</th>
                <th className="py-3.5 px-4 font-medium">Poll Status</th>
                <th className="py-3.5 px-4 font-medium">Enabled</th>
                <th className="py-3.5 px-5 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#22222A] text-[13px]">
              {(data?.sources ?? []).map((src) => {
                const statusBadge =
                  src.last_error
                    ? `ERR: ${src.last_error}`
                    : src.last_status === 304
                      ? `304 Cached (${src.consecutive_304}x)`
                      : src.last_status > 0
                        ? `HTTP ${src.last_status}`
                        : 'Pending'
                return (
                  <tr key={src.id} className="hover:bg-[#1D1D23] transition-colors">
                    <td className="py-3.5 px-5 font-medium text-white">{src.name}</td>
                    <td className="py-3.5 px-4 text-[#8B8B96] max-w-[260px] truncate" title={src.url}>
                      {src.url}
                    </td>
                    <td className="py-3.5 px-4">
                      <Badge>{src.category || 'General'}</Badge>
                    </td>
                    <td className="py-3.5 px-4">
                      <Badge variant={src.tier === 1 ? 'tier1' : src.tier === 2 ? 'tier2' : 'tier3'}>
                        {tierLabel(src.tier)}
                      </Badge>
                    </td>
                    <td className="py-3.5 px-4 tabular-nums">
                      <div className="flex flex-col gap-0.5">
                        <span
                          className={
                            src.last_error
                              ? 'text-[#FCA5A5] text-[12px] truncate max-w-[220px]'
                              : 'text-[#D4D4DC] text-[12px]'
                          }
                          title={src.last_error || statusBadge}
                        >
                          {statusBadge}
                          {src.last_latency_ms > 0 ? ` · ${src.last_latency_ms}ms` : ''}
                        </span>
                        <span className="text-[11px] text-[#6B6B76]">
                          Next: {formatRelativeTime(src.next_poll_at)}
                          {src.etag ? ' · ETag' : ''}
                        </span>
                      </div>
                    </td>
                    <td className="py-3.5 px-4">
                      <Switch
                        checked={src.enabled}
                        onCheckedChange={(next) => void handleToggleEnabled(src, next)}
                        label={`Toggle ${src.name}`}
                      />
                    </td>
                    <td className="py-3.5 px-5 text-right">
                      <div className="inline-flex items-center gap-1.5">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => openEdit(src)}
                          aria-label={`Edit ${src.name}`}
                        >
                          <Edit3 className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => setDeleteTarget(src)}
                          aria-label={`Delete ${src.name}`}
                        >
                          <Trash2 className="w-3.5 h-3.5 text-[#FCA5A5]" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </Card>

      <Dialog
        open={modalMode !== null}
        onClose={() => setModalMode(null)}
        title={modalMode === 'add' ? 'Add RSS/Atom Source' : 'Edit RSS/Atom Source'}
        subtitle="Changes persist directly to SQLite and trigger hot feed reload."
      >
        <form onSubmit={handleSave} className="space-y-4">
          <div>
            <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Publisher Name</label>
            <Input
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="e.g. Reuters World"
            />
          </div>
          <div>
            <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Feed URL</label>
            <Input
              value={form.url}
              onChange={(e) => setForm((f) => ({ ...f, url: e.target.value }))}
              placeholder="https://example.com/rss.xml"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Category</label>
              <Input
                value={form.category}
                onChange={(e) => setForm((f) => ({ ...f, category: e.target.value }))}
                placeholder="Tech / Finance"
              />
            </div>
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Authority Tier</label>
              <Select
                className="w-full"
                value={String(form.tier)}
                onChange={(e) => setForm((f) => ({ ...f, tier: Number(e.target.value) }))}
              >
                <option value="1">Tier 1 (3.0x)</option>
                <option value="2">Tier 2 (2.0x)</option>
                <option value="3">Tier 3+ (1.0x)</option>
              </Select>
            </div>
          </div>
          <div className="flex items-center justify-between pt-1">
            <span className="text-[13px] text-[#D4D4DC]">Enabled for adaptive polling</span>
            <Switch
              checked={form.enabled}
              onCheckedChange={(enabled) => setForm((f) => ({ ...f, enabled }))}
              label="Source enabled"
            />
          </div>
          <div className="flex justify-end gap-2 pt-3 border-t border-[#24242C]">
            <Button variant="ghost" onClick={() => setModalMode(null)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={saving}>
              {saving ? 'Saving...' : modalMode === 'add' ? 'Create Source' : 'Save Changes'}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        title="Delete Feed Source"
        subtitle="Are you sure you want to remove this source from SQLite?"
      >
        <p className="text-[13px] text-[#A0A0AB] mb-5">
          Feed <span className="text-white font-medium">{deleteTarget?.name}</span> (
          {deleteTarget?.url}) will stop being polled immediately.
        </p>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => setDeleteTarget(null)}>
            Cancel
          </Button>
          <Button variant="danger" onClick={handleConfirmDelete} disabled={saving}>
            {saving ? 'Deleting...' : 'Delete Source'}
          </Button>
        </div>
      </Dialog>
    </div>
  )
}
