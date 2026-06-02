# IDUNA · Apples Implementation
## Golden Documentation Log Streaming

**Document ID:** HQ-SPEC-IAM-096  
**Status:** Implementation Ready — kick this to recursive self-improvement  
**License:** Public Domain (Unlicense)

---

## What This Is

**Apples** are golden documentation records — structured reports emitted by any agent or automated process at the end of each recursive self-improvement run. Every iteration that changes something should produce an Apple. Every Apple is streamed to Iduna and stored in the append-only event ledger.

Iduna is the system of record. The Back Office audit viewer is where you read them.

---

## Design Principles

Apples follow the same append-only NDJSON event pattern already established across the ecosystem (FatBaby's eventstore, Iduna's `iam_event_stream`). No new architectural patterns. New table, new endpoint, same shape.

Agents authenticate via the existing M2M credential system (`POST /api/v1/auth/agent`). The resulting JWT carries the agent's identity and permissions into the Apple submission endpoint.

---

## 1. Database Schema

New migration: `migrations/truestore/202606020001_apples.sql`

```sql
-- Apples: golden documentation records from recursive self-improvement runs.
-- Append-only. Never update or delete rows.

CREATE TABLE IF NOT EXISTS `apples` (
    `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `agent_id`       VARCHAR(36)     NOT NULL COMMENT 'FK to agents.id — who wrote this',
    `source_repo`    VARCHAR(255)    NOT NULL COMMENT 'e.g. prrject-fatbaby, emily, iduna',
    `run_id`         VARCHAR(128)    NOT NULL COMMENT 'unique run/iteration identifier (git sha, cron trace_id, etc.)',
    `apple_type`     VARCHAR(64)     NOT NULL COMMENT 'improvement | observation | incident | release | audit',
    `title`          VARCHAR(255)    NOT NULL,
    `body`           MEDIUMTEXT      NOT NULL COMMENT 'markdown — the full golden doc content',
    `metadata`       JSON            NULL     COMMENT 'arbitrary structured context: gear, version, signal counts, etc.',
    `recorded_at`    TIMESTAMP(6)    NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    INDEX `idx_apples_agent`      (`agent_id`),
    INDEX `idx_apples_repo`       (`source_repo`),
    INDEX `idx_apples_type`       (`apple_type`),
    INDEX `idx_apples_recorded`   (`recorded_at`),
    INDEX `idx_apples_run_id`     (`run_id`),
    CONSTRAINT `fk_apples_agent` FOREIGN KEY (`agent_id`) REFERENCES `agents` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

---

## 2. Store Interface

Add to `internal/store/store.go` `IAMStore` interface:

```go
// AppendApple inserts a golden documentation record.
// Emits an ApplePublished event to iam_event_stream.
AppendApple(ctx context.Context, apple AppleRecord) (id int64, err error)

// ListApples returns up to limit apples, most recent first.
// Filter fields are optional (empty string = no filter).
ListApples(ctx context.Context, agentID, sourceRepo, appleType string, limit int) ([]AppleRecord, error)

// GetApple returns a single apple by its integer ID.
GetApple(ctx context.Context, id int64) (*AppleRecord, error)
```

Add to `internal/auth/agent.go`:

```go
// AppleRecord is a golden documentation entry from a self-improvement run.
type AppleRecord struct {
    ID          int64
    AgentID     string
    SourceRepo  string
    RunID       string
    AppleType   string
    Title       string
    Body        string
    Metadata    []byte    // raw JSON
    RecordedAt  time.Time
}
```

---

## 3. API Endpoints

### POST `/api/v1/apples`

Submit a new Apple. Requires a valid agent JWT with `apples.write` permission.

**Request:**
```json
{
    "source_repo": "prrject-fatbaby",
    "run_id":      "trace_abc123",
    "apple_type":  "improvement",
    "title":       "EPS signal extraction: iteration 14",
    "body":        "## Summary\n\nThis run improved...",
    "metadata": {
        "gear": 3,
        "signals_processed": 1042,
        "improvements_committed": 2
    }
}
```

**Response `201 Created`:**
```json
{
    "id": 42,
    "recorded_at": "2026-06-02T14:22:15.000000Z"
}
```

**Response `403 Forbidden`:** agent lacks `apples.write`  
**Response `401 Unauthorized`:** invalid or expired JWT

---

### GET `/api/v1/apples`

List Apples. Requires `apples.read` permission.

**Query params:** `agent_id`, `source_repo`, `apple_type`, `limit` (default 50, max 500)

**Response `200 OK`:**
```json
{
    "apples": [
        {
            "id": 42,
            "agent_id": "age_...",
            "source_repo": "prrject-fatbaby",
            "run_id": "trace_abc123",
            "apple_type": "improvement",
            "title": "EPS signal extraction: iteration 14",
            "recorded_at": "2026-06-02T14:22:15.000000Z"
        }
    ]
}
```

Note: `body` and `metadata` are omitted from list responses. Fetch individually.

---

### GET `/api/v1/apples/{id}`

Full Apple including body and metadata. Requires `apples.read`.

---

## 4. IAM Events

Every `AppendApple` call emits to `iam_event_stream`:

```json
{
    "event_type":      "ApplePublished",
    "aggregate_type":  "AGENT",
    "aggregate_id":    "<agent_id>",
    "operator_id":     "<agent_id>",
    "payload": {
        "apple_id":    42,
        "source_repo": "prrject-fatbaby",
        "run_id":      "trace_abc123",
        "apple_type":  "improvement",
        "title":       "EPS signal extraction: iteration 14"
    }
}
```

This means every Apple is visible in the existing Back Office audit viewer at `/admin/audit` with no additional UI work.

---

## 5. Permissions

Add to the permissions seed data:

| Permission | Description |
|---|---|
| `apples.write` | Submit golden documentation records |
| `apples.read` | Read Apple records and list |
| `apples.admin` | Full access including bulk query and export |

Default grants:

| Role / Agent | Permissions |
|---|---|
| `EMIREE` | `apples.write`, `apples.read` |
| `EMILY_PRIME` | `apples.write`, `apples.read` |
| `FATBABY_EMILY` | `apples.write`, `apples.read` |
| `SUPER_ADMIN` | `apples.admin` |
| `ANALYST` | `apples.read` |

---

## 6. Back Office UI

Add to `AdminHandler.Init()`:

```go
h.mux.HandleFunc("/admin/apples",   h.apples)
h.mux.HandleFunc("/admin/apples/",  h.appleDetail)
```

**Apples ledger view** (`/admin/apples`): filterable by source repo, agent, type. Tabular, monospace, same ledger aesthetic as the audit viewer. Columns: `id`, `recorded_at`, `source_repo`, `agent`, `type`, `title`.

**Apple detail view** (`/admin/apples/{id}`): renders the `body` field as preformatted markdown-in-pre (no client-side rendering — keep it archival). Shows full metadata JSON block below.

---

## 7. Apple Types

| `apple_type` | When to use |
|---|---|
| `improvement` | A self-improvement iteration completed — code changed, tests passed, committed |
| `observation` | A notable finding that didn't result in immediate code change |
| `incident` | Something went wrong — what, why, resolution |
| `release` | A version or milestone cut |
| `audit` | Periodic system health summary |

---

## 8. How Agents Submit

Agents already have M2M credentials. The submission flow:

```
1. Agent authenticates: POST /api/v1/auth/agent
   → receives IDUNA JWT with apples.write permission

2. At end of each RSI run, agent POSTs to /api/v1/apples
   → body is the full markdown report
   → metadata carries run-specific structured data
   → Iduna appends to apples table + emits ApplePublished event

3. Done. Git is still source of truth for code.
   Iduna is source of truth for what the run produced and reported.
```

---

## 9. Implementation Checklist for Claude Code

```
[ ] migrations/truestore/202606020001_apples.sql
[ ] Add AppleRecord type to internal/auth/agent.go
[ ] Add AppendApple / ListApples / GetApple to IAMStore interface
[ ] Implement in internal/store/mysql.go
    - AppendApple: INSERT + AppendIAMEvent in transaction
    - ListApples: SELECT with optional filters
    - GetApple: SELECT by id
[ ] internal/http/handlers/apples.go
    - POST /api/v1/apples (requires apples.write)
    - GET  /api/v1/apples (requires apples.read)
    - GET  /api/v1/apples/{id} (requires apples.read)
[ ] Wire routes in main.go
[ ] Add apples.write / apples.read / apples.admin to permissions seed
[ ] Admin UI: /admin/apples ledger view
[ ] Admin UI: /admin/apples/{id} detail view
[ ] go test ./...
[ ] Update CHANGELOG.md
[ ] Publish an Apple from the apple endpoint to confirm the loop closes
```

---

## 10. The Loop Closes

When this is implemented, the recursive self-improvement cycle has a complete paper trail:

```
RSI run executes
    → code changes committed to Git (source of truth for what changed)
    → Apple posted to Iduna (source of truth for what the run reported)
    → ApplePublished event in iam_event_stream (audit ledger)
    → visible in Back Office audit viewer immediately
    → visible in Back Office apples ledger with full body

Next run reads previous Apple as context if needed.
The witch's footprints are preserved.
```
