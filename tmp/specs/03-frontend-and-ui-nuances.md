# Spec 03: Frontend Architecture & UI Nuances

Guidelines for translating the HTML mockups in `tmp/*.html` into the production React + TypeScript + Vite + Tailwind CSS + `shadcn/ui` SPA under `web/`.

## 1. Visual Fidelity vs. Full-Screen Viewport Adaptation

The 6 files in `tmp/` (`overview.html`, `intelligence.html`, `raw-feed.html`, `sources.html`, `dispatch.html`, `settings.html`) define the exact dark monochrome Bento visual language:
1. Palette: `#0B0B0D` background, `#121215` shell, `#18181C` cards, `#24242C` / `#26262E` hairline `1px` borders, crisp white active pills (`bg-white text-zinc-950`), and `tabular-nums` typography.
2. **Full-Screen Layout Fix**: The static mockups wrap the page in `max-w-[1440px]` centered inside `body flex items-center justify-center`. In the production React app, remove the cramped vertical centering so the application fills the viewport (`min-h-screen w-full`) cleanly on desktop and ultrawide monitors while keeping comfortable responsive container padding.

## 2. Mobile Navigation Fix (Icon-Only on Small Screens)

In `tmp/*.html`, the top `<nav>` renders 6 text pill links (`Overview`, `Intelligence`, `Raw Feed`, `Sources`, `Dispatch`, `Settings`) inside a horizontal row. On mobile viewports, those text pills crowd and break the header layout.
Implement responsive navigation pills in the shared top bar component:
1. **Mobile (`< md` / `< 768px`)**: Render each navigation tab as a compact square/rounded **icon button** (`w-10 h-10`, no text label) using Lucide icons (`LayoutDashboard` for Overview, `Sparkles` or `Layers` for Intelligence, `Rss` for Raw Feed, `Database` or `Globe` for Sources, `Send` for Dispatch, `Sliders` or `Settings` for Settings) with `aria-label` and `title`.
2. **Desktop (`>= md`)**: Render the pill tabs with their text labels (or icon + text label) exactly as shown in `tmp/*.html`.

## 3. Page-Specific Implementation Nuances

### 3.1 Overview (`tmp/overview.html`)
1. **Dynamic SVG Gauges & Waves**: Make the Conditional Poll Cache semi-circular arc (`stroke-dasharray` / arc endpoint) and the barcode progress strips reflect live percentages from `GET /api/overview`.
2. **Window & Tier Filter Pills**: Clicking the Tier pill in the hero banner (`All Tiers`, `Tier 1`, `Tier 2`, `Tier 3`) filters the Overview metrics and Top Clusters.
3. **Right-Panel Quick Actions**: Wire `Sources` (navigate to `/sources`), `Raw RSS` (navigate to `/raw-feed`), and `Flush Now` (`POST /api/dispatch/flush`).
4. **Right-Panel Bottom Input & Send Button (Deferred Feature)**: Lines 585-597 of `tmp/overview.html` include a text input (`"Filter or inspect cluster..."`), a `+` button, and a `send` button. Because LLM tool-calling / SQL querying is **deferred**, wire the text input to filter the displayed cluster list client-side and mark the `+` / `send` buttons as disabled with a `"Interactive AI query coming soon"` tooltip.

### 3.2 Intelligence Feed (`tmp/intelligence.html`)
1. Wire filter pills (`All Clusters`, `Breaking >= 12.0`, `Tier 1 Lead`), sort selector (`Velocity Score` vs. `Most Recent`), and right-sidebar `Highest Publisher Tier` filter buttons.
2. Each cluster card must support expanding/collapsing its underlying verified source articles drawer with outbound source links.

### 3.3 Raw RSS Feed (`tmp/raw-feed.html`)
1. **Toolbar Additions (Issue #13)**: In addition to the `Category` dropdown shown in `tmp/raw-feed.html`, include a `Source` filter dropdown and a **`Show Thumbnails` toggle switch** in the hero toolbar.
2. **Thumbnail Rendering**: When `Show Thumbnails` is enabled and an article has a non-empty `image_url`, render a clean right-aligned thumbnail (`w-20 h-14 object-cover rounded-[10px] border border-[#2A2A32]`) with an `onError` handler that hides broken image URLs gracefully.
3. **Title-Only Row Fallback**: When `description` is empty or identical to `title` (like items 2 and 5 in `tmp/raw-feed.html`), render the compact single-line headline row (`py-4`) with zero empty paragraph spacing.

### 3.4 Sources & Categories (`tmp/sources.html`)
1. Wire `Import YAML` (file picker -> `POST /api/config/yaml`), `Export YAML` (triggers download from `GET /api/config/yaml`), and `Add Source` / `Edit` / `Delete` modals.
2. Wire the inline `Enabled` toggle switch on each row to `PUT /api/sources/{id}`.

### 3.5 Dispatch Queue & History (`tmp/dispatch.html`)
1. Wire the two-tab switcher (`Pending & Retrying` vs. `Dispatched History`) and `Flush Buffer Now` button.
2. Wire `Retry Immediately` (`POST /api/dispatch/retries/{id}/retry`) and `Cancel` (`DELETE /api/dispatch/retries/{id}`) on each active retry card.

### 3.6 Settings & Doctor (`tmp/settings.html`)
1. **Live Model Discovery**: Clicking `Fetch Models` calls `POST /api/models/discover` using the currently entered provider and API key, updating the searchable model selector and count label (`"N models fetched from /v1/models"`).
2. **Strict Inline Validation**: Replicate and enforce the inline error state from `tmp/settings.html` lines 195-210 for both Crawler (`jina` / `firecrawl` without API key) and LLM (when enabled with a cloud provider and blank API key), blocking `Save Configuration` until resolved.
3. **System Doctor**: Clicking `Run Doctor` calls `POST /api/doctor`, shows a loading spinner on the button, and renders live `PASS` / `FAIL` badges and failure details across all 6 stages.
