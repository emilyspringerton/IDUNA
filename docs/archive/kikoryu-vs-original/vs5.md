VS5: “Follow My Trades” Output Streams (Event Stream API)
VS5 introduces the observable social layer to the IDUNA ecosystem by exposing a sanitized, permissioned, and replayable event stream of trading activity derived from the VS3 Market Game
OBOBOB. This service allows players to share their trading activity with others, enabling the creation of "trade tapes," social feeds, and institutional dashboards
.
1. Core Purpose and Philosophy
The goal of VS5 is to transform internal game actions into issued records—stamped, attributable feeds that function as a "Recognized Narrative of Reality" for the market simulation
. It is explicitly framed as an entertainment-only game event export, not as financial advice or real-market execution
.
Internal Event Store: Maintains full-fidelity, immutable records for system correctness
.
Export Stream: Provides a curated, privacy-safe, long-lived schema for public consumption
.
2. Technical Specification
The export stream is managed through an Export Projector that listens to internal trade events and writes them to an export_outbox table to ensure durable ordering and replayability
.
Event Payload (TradeFilled MVP)
For the initial release, the system focuses on the TradeFilled event type
. Required fields include:
Identities: event_id (UUID), user_id (pseudonymous), and handle (if permitted)
.
Market Data: symbol (canonical ticker), side (BUY/SELL), qty, price, and notional
.
Metadata: occurred_at (UTC timestamp), season_id, and source_version (schema version)
.
Integrity: An optional HMAC signature (sig) for payload verification
.
API Surface
Stream Management: Authenticated endpoints (POST /streams, PATCH /streams/me) to allow users to create and configure their streams
.
Read Access: Support for Cursor-based pagination for replaying events and Server-Sent Events (SSE) for real-time updates
. Webhooks are deferred to the VS6 release
.
3. Privacy and Safety Controls
Given the competitive nature of the market game, strict non-negotiable rules are enforced:
Opt-in Only: Streams are disabled by default
.
Anti-Sniping Delay: To prevent players from simply "live-copying" top traders, an optional delay (default 60s, up to 900s) is introduced
.
PII Sanitization: Explicitly forbids the export of emails, IP/device data, or internal account IDs
.
Disclaimer Enforcement: Every stream and UI surface must carry the disclaimer: "Virtual trades. Entertainment only. Not investment advice"
.
4. Visual Identity: The "Issued Paper" Layer
The VS5 user interface adheres to the "Back Office" aesthetic—representing a clerical authority that catalogs the world's movements
.
Visual Metaphor: The settings screen should feel like an archive authorization form or a stamped registry entry
.
The Semantics of Metal: While structural elements use Gold (#C6A75E), Rose Gold (#B76E79) is used exclusively for the irrevocable consent toggles required to enable a public trade tape
.
Typography: Ceremonial labels and headers use Garamond, while the dense metadata of the trade tape itself uses Helvetica for functional clarity
.
5. VS5 "Done" Definition
A successful implementation of VS5 is verified when:
Users can explicitly enable a trade stream with defined visibility (public/followers/private)
.
TradeFilled events appear in the stream accurately after the configured delay
.
Cursor-based replay and SSE polling work reliably for public consumers
.
No PII is leaked into the public export
.
Administrative Agents (VS7) can audit the export stream and manage user permissions via the Back Office
.
