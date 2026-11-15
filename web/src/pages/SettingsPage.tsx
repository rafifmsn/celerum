import { useCallback, useEffect, useMemo, useState } from 'react'
import { CheckCircle2, Loader2, Plus, RefreshCw, Save, Stethoscope, X, XCircle } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { ApiError, discoverModels, fetchSettings, runDoctor, saveSettings } from '@/lib/api'
import { cn, formatRelativeTime } from '@/lib/utils'
import type { DoctorReport, SettingsPayload } from '@/types/api'

export function SettingsPage() {
  const [savedSnapshot, setSavedSnapshot] = useState<SettingsPayload | null>(null)
  const [draft, setDraft] = useState<SettingsPayload | null>(null)
  const [models, setModels] = useState<string[]>([])
  const [modelSearch, setModelSearch] = useState('')
  const [discoverStatus, setDiscoverStatus] = useState<string | null>(null)
  const [discovering, setDiscovering] = useState(false)
  const [saving, setSaving] = useState(false)
  const [validationError, setValidationError] = useState<string | null>(null)
  const [saveSuccess, setSaveSuccess] = useState<string | null>(null)
  const [newKeyword, setNewKeyword] = useState('')
  const [doctorReport, setDoctorReport] = useState<DoctorReport | null>(null)
  const [runningDoctor, setRunningDoctor] = useState(false)

  const load = useCallback(async () => {
    const res = await fetchSettings()
    const normalized: SettingsPayload = {
      ...res,
      keywords: {
        boost: res.keywords?.boost ?? [],
        blocklist: res.keywords?.blocklist ?? [],
      },
      enrichment: {
        scraper: res.enrichment?.scraper ?? 'none',
        jina_api_key: res.enrichment?.jina_api_key ?? '',
        firecrawl_api_key: res.enrichment?.firecrawl_api_key ?? '',
      },
      llm: {
        enabled: Boolean(res.llm?.enabled),
        provider: res.llm?.provider ?? 'openai',
        base_url: res.llm?.base_url ?? '',
        model: res.llm?.model ?? '',
        api_key: res.llm?.api_key ?? '',
        language: res.llm?.language ?? 'en',
        system_prompt: res.llm?.system_prompt ?? '',
      },
      dispatch: {
        telegram: {
          enabled: Boolean(res.dispatch?.telegram?.enabled),
          bot_token: res.dispatch?.telegram?.bot_token ?? '',
          chat_id: res.dispatch?.telegram?.chat_id ?? '',
        },
        webhook: {
          enabled: Boolean(res.dispatch?.webhook?.enabled),
          url: res.dispatch?.webhook?.url ?? '',
          headers: res.dispatch?.webhook?.headers ?? {},
        },
      },
    }
    setSavedSnapshot(normalized)
    setDraft(structuredClone(normalized))
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const filteredModels = useMemo(() => {
    const q = modelSearch.trim().toLowerCase()
    if (!q) return models
    return models.filter((m) => m.toLowerCase().includes(q))
  }, [models, modelSearch])

  const validateClient = (cfg: SettingsPayload): string | null => {
    const scraper = cfg.enrichment.scraper.toLowerCase()
    if (scraper === 'jina' && !cfg.enrichment.jina_api_key.trim()) {
      return 'Jina scraper selected: a Jina API key is required.'
    }
    if (scraper === 'firecrawl' && !cfg.enrichment.firecrawl_api_key.trim()) {
      return 'Firecrawl scraper selected: a Firecrawl API key is required.'
    }
    if (cfg.llm.enabled && cfg.llm.provider.toLowerCase() !== 'ollama' && !cfg.llm.api_key.trim()) {
      return `LLM provider "${cfg.llm.provider}" requires an API key when enabled.`
    }
    return null
  }

  const handleSave = async () => {
    if (!draft) return
    setValidationError(null)
    setSaveSuccess(null)
    const errMsg = validateClient(draft)
    if (errMsg) {
      setValidationError(errMsg)
      return
    }
    setSaving(true)
    try {
      await saveSettings(draft)
      setSavedSnapshot(structuredClone(draft))
      setSaveSuccess('Configuration saved and hot-reloaded.')
    } catch (err) {
      if (err instanceof ApiError) {
        setValidationError(err.message)
      } else {
        setValidationError(err instanceof Error ? err.message : 'Save failed')
      }
    } finally {
      setSaving(false)
    }
  }

  const handleDiscard = () => {
    if (!savedSnapshot) return
    setDraft(structuredClone(savedSnapshot))
    setValidationError(null)
    setSaveSuccess(null)
  }

  const handleDiscoverModels = async () => {
    if (!draft) return
    setDiscovering(true)
    setDiscoverStatus(null)
    setValidationError(null)
    try {
      const res = await discoverModels({
        provider: draft.llm.provider,
        base_url: draft.llm.base_url,
        api_key: draft.llm.api_key,
      })
      setModels(res.models)
      setDiscoverStatus(`${res.models.length} models fetched from ${res.endpoint}`)
      if (res.models.length > 0 && !res.models.includes(draft.llm.model)) {
        setDraft((prev) =>
          prev ? { ...prev, llm: { ...prev.llm, model: res.models[0] } } : prev,
        )
      }
    } catch (err) {
      setDiscoverStatus(err instanceof Error ? err.message : 'Model discovery failed')
    } finally {
      setDiscovering(false)
    }
  }

  const handleRunDoctor = async () => {
    setRunningDoctor(true)
    try {
      const report = await runDoctor(false, draft ?? undefined)
      setDoctorReport(report)
    } finally {
      setRunningDoctor(false)
    }
  }

  const handleAddKeyword = () => {
    const kw = newKeyword.trim()
    if (!kw || !draft) return
    if (draft.keywords.boost.includes(kw)) {
      setNewKeyword('')
      return
    }
    setDraft({
      ...draft,
      keywords: { ...draft.keywords, boost: [...draft.keywords.boost, kw] },
    })
    setNewKeyword('')
  }

  const handleRemoveKeyword = (kw: string) => {
    if (!draft) return
    setDraft({
      ...draft,
      keywords: {
        ...draft.keywords,
        boost: draft.keywords.boost.filter((k) => k !== kw),
      },
    })
  }

  if (!draft) {
    return (
      <Card className="p-8 text-center text-[13px] text-[#7A7A86]">
        Loading Celerum configuration...
      </Card>
    )
  }

  return (
    <div className="grid grid-cols-1 xl:grid-cols-12 gap-5 items-start">
      <div className="xl:col-span-8 flex flex-col gap-5">
        <Card className="p-5 flex flex-wrap items-center justify-between gap-4">
          <h1 className="text-[20px] font-semibold text-white">Pipeline Settings & Secrets</h1>
          <div className="flex items-center gap-2.5">
            <Button variant="ghost" onClick={handleDiscard}>
              Discard Changes
            </Button>
            <Button variant="primary" onClick={handleSave} disabled={saving}>
              <Save className="w-3.5 h-3.5" />
              {saving ? 'Saving...' : 'Save Configuration'}
            </Button>
          </div>
        </Card>

        {validationError && (
          <div className="px-4 py-3 rounded-2xl bg-[#221618] border border-[#3E2428] text-[13px] text-[#FCA5A5]">
            {validationError}
          </div>
        )}

        {saveSuccess && (
          <div className="px-4 py-3 rounded-2xl bg-[#161B18] border border-[#26382B] text-[13px] text-[#C5E8CF]">
            {saveSuccess}
          </div>
        )}

        <Card className="p-5 space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-[15px] font-semibold text-white">LLM Synthesis Provider</h2>
              <p className="text-[12px] text-[#7A7A86]">
                Generates editorial single-paragraph summaries for Breaking clusters.
              </p>
            </div>
            <div className="flex items-center gap-2.5">
              <span className="text-[12px] text-[#9A9AA6]">Enabled</span>
              <Switch
                checked={draft.llm.enabled}
                onCheckedChange={(enabled) =>
                  setDraft({ ...draft, llm: { ...draft.llm, enabled } })
                }
                label="Enable LLM synthesis"
              />
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Provider</label>
              <Select
                className="w-full"
                value={draft.llm.provider}
                onChange={(e) =>
                  setDraft({ ...draft, llm: { ...draft.llm, provider: e.target.value } })
                }
              >
                <option value="openai">OpenAI / Compatible</option>
                <option value="anthropic">Anthropic</option>
                <option value="groq">Groq</option>
                <option value="openrouter">OpenRouter</option>
                <option value="gemini">Google Gemini</option>
                <option value="ollama">Ollama (Local)</option>
              </Select>
            </div>

            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Base URL (optional)</label>
              <Input
                value={draft.llm.base_url}
                onChange={(e) =>
                  setDraft({ ...draft, llm: { ...draft.llm, base_url: e.target.value } })
                }
                placeholder="Auto-resolved by provider"
              />
            </div>

            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">
                API Key {draft.llm.enabled && draft.llm.provider !== 'ollama' ? '(required)' : ''}
              </label>
              <Input
                type="password"
                value={draft.llm.api_key}
                onChange={(e) =>
                  setDraft({ ...draft, llm: { ...draft.llm, api_key: e.target.value } })
                }
                placeholder="sk-..."
              />
            </div>

            <div className="min-w-0">
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Model</label>
              <div className="flex items-center gap-2 min-w-0">
                {models.length > 0 ? (
                  <Select
                    className="flex-1 min-w-0 w-full truncate"
                    value={draft.llm.model}
                    onChange={(e) =>
                      setDraft({ ...draft, llm: { ...draft.llm, model: e.target.value } })
                    }
                  >
                    {!models.includes(draft.llm.model) && draft.llm.model && (
                      <option value={draft.llm.model}>{draft.llm.model}</option>
                    )}
                    {filteredModels.map((m) => (
                      <option key={m} value={m}>
                        {m}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <Input
                    className="flex-1 min-w-0"
                    value={draft.llm.model}
                    onChange={(e) =>
                      setDraft({ ...draft, llm: { ...draft.llm, model: e.target.value } })
                    }
                    placeholder="gpt-4o-mini"
                  />
                )}
                <Button
                  variant="secondary"
                  className="shrink-0 whitespace-nowrap"
                  onClick={handleDiscoverModels}
                  disabled={discovering}
                >
                  <RefreshCw className={cn('w-3.5 h-3.5', discovering && 'animate-spin')} />
                  Fetch Models
                </Button>
              </div>
              {models.length > 5 && (
                <Input
                  className="mt-2"
                  value={modelSearch}
                  onChange={(e) => setModelSearch(e.target.value)}
                  placeholder="Filter discovered models..."
                />
              )}
              {discoverStatus && (
                <p className="text-[11px] text-[#9A9AA6] mt-1.5">{discoverStatus}</p>
              )}
            </div>
          </div>
        </Card>

        <Card className="p-5 space-y-4">
          <div>
            <h2 className="text-[15px] font-semibold text-white">Article Enrichment Scraper</h2>
            <p className="text-[12px] text-[#7A7A86]">
              Strictly enforces valid API credentials when Jina or Firecrawl is active.
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Scraper Mode</label>
              <Select
                className="w-full"
                value={draft.enrichment.scraper}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    enrichment: { ...draft.enrichment, scraper: e.target.value },
                  })
                }
              >
                <option value="none">RSS Description Only (none)</option>
                <option value="jina">Jina Reader (jina)</option>
                <option value="firecrawl">Firecrawl (firecrawl)</option>
              </Select>
            </div>

            {draft.enrichment.scraper === 'jina' && (
              <div>
                <label className="block text-[12px] text-[#9A9AA6] mb-1.5">
                  Jina API Key (required)
                </label>
                <Input
                  type="password"
                  value={draft.enrichment.jina_api_key}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      enrichment: { ...draft.enrichment, jina_api_key: e.target.value },
                    })
                  }
                  placeholder="jina_..."
                />
              </div>
            )}

            {draft.enrichment.scraper === 'firecrawl' && (
              <div>
                <label className="block text-[12px] text-[#9A9AA6] mb-1.5">
                  Firecrawl API Key (required)
                </label>
                <Input
                  type="password"
                  value={draft.enrichment.firecrawl_api_key}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      enrichment: { ...draft.enrichment, firecrawl_api_key: e.target.value },
                    })
                  }
                  placeholder="fc-..."
                />
              </div>
            )}
          </div>
        </Card>

        <Card className="p-5 space-y-4">
          <h2 className="text-[15px] font-semibold text-white">Engine Timing, Scoring & Keywords</h2>

          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Poll Interval</label>
              <Input
                value={draft.engine.poll_interval}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    engine: { ...draft.engine, poll_interval: e.target.value },
                  })
                }
              />
            </div>
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Flush Interval</label>
              <Input
                value={draft.engine.flush_interval}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    engine: { ...draft.engine, flush_interval: e.target.value },
                  })
                }
              />
            </div>
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Sliding Window</label>
              <Input
                value={draft.engine.window_duration}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    engine: { ...draft.engine, window_duration: e.target.value },
                  })
                }
              />
            </div>
            <div>
              <label className="block text-[12px] text-[#9A9AA6] mb-1.5">Breaking Score</label>
              <Input
                type="number"
                step="0.5"
                value={draft.engine.breaking_threshold}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    engine: { ...draft.engine, breaking_threshold: Number(e.target.value) },
                  })
                }
              />
            </div>
          </div>

          <div className="space-y-2 pt-2">
            <label className="block text-[12px] text-[#9A9AA6]">Boosted Keywords</label>
            <div className="flex flex-wrap gap-2">
              {draft.keywords.boost.map((kw) => (
                <span
                  key={kw}
                  className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full bg-[#121215] border border-[#2A2A34] text-[12px] text-[#E4E4EA]"
                >
                  {kw}
                  <button
                    type="button"
                    onClick={() => handleRemoveKeyword(kw)}
                    aria-label={`Remove ${kw}`}
                    className="text-[#7A7A86] hover:text-white cursor-pointer"
                  >
                    <X className="w-3 h-3" />
                  </button>
                </span>
              ))}
            </div>
            <div className="flex gap-2 pt-1 max-w-sm">
              <Input
                value={newKeyword}
                onChange={(e) => setNewKeyword(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    handleAddKeyword()
                  }
                }}
                placeholder="Add boosted keyword..."
              />
              <Button variant="secondary" onClick={handleAddKeyword}>
                <Plus className="w-3.5 h-3.5" />
                Add
              </Button>
            </div>
          </div>
        </Card>

        <Card className="p-5 space-y-4">
          <h2 className="text-[15px] font-semibold text-white">Dispatch Targets</h2>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="p-4 rounded-2xl bg-[#121215] border border-[#22222A] space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-[13px] font-medium text-white">Telegram Bot</span>
                <Switch
                  checked={draft.dispatch.telegram.enabled}
                  onCheckedChange={(enabled) =>
                    setDraft({
                      ...draft,
                      dispatch: {
                        ...draft.dispatch,
                        telegram: { ...draft.dispatch.telegram, enabled },
                      },
                    })
                  }
                  label="Telegram enabled"
                />
              </div>
              <Input
                type="password"
                value={draft.dispatch.telegram.bot_token}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    dispatch: {
                      ...draft.dispatch,
                      telegram: { ...draft.dispatch.telegram, bot_token: e.target.value },
                    },
                  })
                }
                placeholder="Bot Token"
              />
              <Input
                value={draft.dispatch.telegram.chat_id}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    dispatch: {
                      ...draft.dispatch,
                      telegram: { ...draft.dispatch.telegram, chat_id: e.target.value },
                    },
                  })
                }
                placeholder="Chat ID"
              />
            </div>

            <div className="p-4 rounded-2xl bg-[#121215] border border-[#22222A] space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-[13px] font-medium text-white">Custom Webhook</span>
                <Switch
                  checked={draft.dispatch.webhook.enabled}
                  onCheckedChange={(enabled) =>
                    setDraft({
                      ...draft,
                      dispatch: {
                        ...draft.dispatch,
                        webhook: { ...draft.dispatch.webhook, enabled },
                      },
                    })
                  }
                  label="Webhook enabled"
                />
              </div>
              <Input
                value={draft.dispatch.webhook.url}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    dispatch: {
                      ...draft.dispatch,
                      webhook: { ...draft.dispatch.webhook, url: e.target.value },
                    },
                  })
                }
                placeholder="https://hooks.example.com/celerum"
              />
            </div>
          </div>
        </Card>
      </div>

      <aside className="xl:col-span-4 min-w-0 xl:sticky xl:top-6">
        <Card className="p-5 space-y-4 overflow-hidden">
          <div className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-2 min-w-0">
              <Stethoscope className="w-4 h-4 text-white shrink-0" />
              <h2 className="text-[15px] font-semibold text-white truncate">System Doctor Diagnostics</h2>
            </div>
            <Button size="sm" variant="primary" onClick={handleRunDoctor} disabled={runningDoctor} className="shrink-0">
              {runningDoctor ? (
                <>
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  Running...
                </>
              ) : (
                'Run Doctor'
              )}
            </Button>
          </div>

          {!doctorReport ? (
            <div className="p-6 rounded-2xl bg-[#121215] border border-[#22222A] text-center text-[12px] text-[#7A7A86]">
              Click &ldquo;Run Doctor&rdquo; to verify Store, Feeds, Scraper, LLM, and Dispatch targets.
            </div>
          ) : (
            <div className="space-y-3">
              <div className="flex items-center justify-between text-[12px] pb-2 border-b border-[#22222A]">
                <span className="text-[#8B8B96] tabular-nums">
                  Checked {formatRelativeTime(doctorReport.checked_at)}
                </span>
                <Badge variant={doctorReport.ok ? 'breaking' : 'muted'}>
                  {doctorReport.ok ? 'ALL PASS' : 'CHECK FAILED'}
                </Badge>
              </div>

              {doctorReport.stages.map((stage) => {
                const passed = stage.status.toUpperCase() === 'PASS'
                return (
                  <div
                    key={stage.stage}
                    className="p-3.5 rounded-2xl bg-[#121215] border border-[#24242C] space-y-2 min-w-0 overflow-hidden"
                  >
                    <div className="flex items-center justify-between gap-2 min-w-0">
                      <div className="flex items-center gap-2 min-w-0">
                        {passed ? (
                          <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
                        ) : (
                          <XCircle className="w-4 h-4 text-rose-400 shrink-0" />
                        )}
                        <span className="text-[13px] font-medium text-white truncate">
                          {stage.stage}. {stage.name}
                        </span>
                      </div>
                      <Badge variant={passed ? 'default' : 'muted'} className="shrink-0">
                        {stage.status} &middot; {stage.latency_ms}ms
                      </Badge>
                    </div>

                    <p
                      title={stage.summary}
                      className="text-[12px] text-[#A0A0AB] line-clamp-2 [overflow-wrap:anywhere]"
                    >
                      {stage.summary}
                    </p>

                    {stage.detail && (
                      <div
                        title={stage.detail}
                        className="px-3 py-2 rounded-xl bg-[#221618] border border-[#3E2428] text-[11px] text-[#FCA5A5] [overflow-wrap:anywhere]"
                      >
                        {stage.detail}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </Card>
      </aside>
    </div>
  )
}
