# System Limitations & Edge Cases

This document outlines known operational boundaries, scraper constraints, and edge cases across the Celerum pipeline.

## 1. Direct Scraper Limitations

The built-in direct scraper (`scraper: "direct"`) performs raw HTTP GET requests followed by basic HTML tag stripping.
It is intended as a lightweight zero-dependency extraction fallback, not a full-featured headless crawler.

- **No Request Throttling or IP Rotation:**
  The direct scraper does not implement domain-level rate limiting, adaptive backoff, or rotating proxies.
  If multiple articles in an active cluster originate from the same domain, concurrent requests can trigger HTTP 429 Too Many Requests or transient IP blocks.
  Users deploying Celerum against rate-sensitive publishers should ensure external proxying or rely on managed scraper backends.

- **No Anti-Bot or CAPTCHA Bypass:**
  Publishers fronted by Cloudflare Bot Management, Datadome, Akamai, or custom WAFs will reject direct scraping requests with HTTP 403 Forbidden or challenge roadblocks.
  Celerum does not solve browser challenges or execute JavaScript.

- **JavaScript-Rendered Single-Page Applications (SPAs):**
  Direct scraping only reads the initial static HTML response.
  Publishers relying entirely on client-side React, Next.js, Vue, or Angular hydration for article text will return empty content or placeholder loading skeletons.

## 2. Paywalled and Protected Content Ingestion

- **Paywall Boilerplate Pollution:**
  When targeting paywalled publications, direct requests frequently return subscription prompts, login walls, or partial teaser snippets.
  Because the scraper extracts all readable text from the response, paywall notices may be passed into the LLM synthesis prompt.
  While the system prompt instructs the model to ignore boilerplate, heavily obfuscated or dominant paywall text can degrade summary quality.

- **Protected RSS and Atom Feeds:**
  Although rare, some publishers place their syndication feeds behind Cloudflare Under Attack mode, geo-blocking, or strict HTTP headers.
  Such feeds may fail conditional polling unless whitelisted or accessed through intermediate network tunnels.
