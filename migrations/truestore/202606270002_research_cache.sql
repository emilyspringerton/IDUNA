-- S137-03: Research result cache
-- Stores Emily Prime research results keyed by normalized query hash.
-- Emily checks cache before fetching; hits return immediately.
-- Expired entries are not deleted automatically — Emily Prime garbage-collects on miss.

CREATE TABLE IF NOT EXISTS research_cache (
    query_hash   VARCHAR(64)  NOT NULL,
    query_text   TEXT         NOT NULL,
    result_json  TEXT         NOT NULL,
    source_urls  TEXT         NOT NULL DEFAULT '',
    sourced_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   DATETIME     NOT NULL,
    PRIMARY KEY (query_hash)
);

CREATE INDEX IF NOT EXISTS idx_research_cache_expires ON research_cache(expires_at);
