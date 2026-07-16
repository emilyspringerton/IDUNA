VS4 — IDUNA “Archive Intake” API Gateway (Upstream Data Proxy)
VS4 is a dedicated API Gateway and Proxy service, known as the "Archive Intake," designed to consume upstream market and news data to feed the VS3 paper market game with institutional, archival knowledge streams
. It serves as the institutional archive ingest layer for all market and news context within the IDUNA ecosystem
.
1. Mission and Core Purpose
The system is built to pull data from multiple upstream sources, normalize it into canonical internal formats, and store it as events and time-series data
. It serves this data to clients via a stable, read-only API that enforces rate limits, caching, provenance, and auditability
.
Design Metaphor: The system is modeled after multi-level library shelves (multiple upstreams), where librarians (normalizers) curate an index (search endpoints) and projectors (curation) determine what becomes visible to players
.
2. Functional Scope
VS4 adds two primary data streams to the platform:
Market Data Ingest: Supporting VS3 with daily OHLC (open/high/low/close) prices, corporate actions (splits/dividends via adjusted close), and symbol metadata such as exchange and sector
.
News Ingest: Augmenting VS3 with per-symbol news streams, macro market-wide news, trending topics, and future sentiment analysis
.
Provenance and Integrity: Every ingested item must carry an upstream source ID, the original upstream URL, a fetched_at timestamp, and a canonical hash for deduplication
.
3. Architecture and Operational Requirements
VS4 stands as a third architectural pillar alongside IAM and game logic
.
Core Components: It consists of Ingest Workers for scheduled pulls and retries, a Normalizer for transforming upstream formats, a Store for time-series and document tables, and a Public Read API
.
Upstream Strategy: Use of per-upstream adapters (e.g., upstream_alpha.go) ensures that all sources are converted into canonical structs
.
Deduplication: The primary deduplication method is a content hash (SHA256 of the normalized headline, URL, and published timestamp)
.
Non-negotiable Constraints: The service must be server-side only, prioritize cache-first responses, and never track users in upstream calls
.
4. API and Data Models
The public-facing API is read-only and includes endpoints for market symbols, daily prices, latest news, and source metadata
.
Canonical Models: The Symbol model maps internal IDs to upstream tickers, while Price bars and News items store normalized data with mandatory attribution
.
Caching Headers: To ensure performance, all responses must include ETag, Cache-Control, and Last-Modified headers
.
5. System Synergies and Visual Identity
VS3 Integration: VS4 provides the data for the "Market" and "Portfolio" pages in the paper market game
. A critical fairness rule dictates that seasons must be evaluated based on stored price snapshots rather than live recomputation to prevent revisionism
.
Visual Tone: The UI must remain institutional and archival, utilizing ledger lists and archive browsing while strictly avoiding flashy, "breaking-news" aesthetics
.
6. "Done" Definition
A successful VS4 implementation requires:
Reliable ingestion of daily prices for a defined universe
.
News ingestion from at least two upstreams (pragmatically starting with RSS-first plus one API provider)
.
Successful backfill capabilities for season evaluation
.
An Admin interface to inspect ingest runs and symbol mappings
.
