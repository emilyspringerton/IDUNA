-- GFD Item Builder batch-propose queue (ITEM_BUILDER_NORTHSTAR.md Phase 2d). Founder real-time:
-- "can we also build a vertex powered assistant where i can drop a list of item names onto a
-- textarea and hit go and it like does a batch add with totally halucinated whatever it thinks
-- stats... proposing items into a queue where we can review and approve them and edit them and
-- approve or just reject if we decide against it."
--
-- Real, deliberate design: an AI-proposed item is NEVER written straight into data/items.json --
-- it lands here as a real, reviewable row first. proposed_json is the full candidate ItemDef
-- (GfdItemDef-shaped), stored as text so a human can edit individual fields before approval
-- without a schema migration every time the AI's own output shape drifts slightly. status:
-- 'pending' (awaiting review) | 'approved' (committed into items.json, kept here for audit,
-- never deleted) | 'rejected' (declined, also kept for audit -- a real record of what was
-- proposed and turned down, not silently discarded).
CREATE TABLE IF NOT EXISTS gfd_item_proposals (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    item_name       VARCHAR(200) NOT NULL,   -- the real input name this proposal was generated from
    proposed_json   TEXT NOT NULL,           -- full candidate GfdItemDef, as JSON text
    status          VARCHAR(16) NOT NULL DEFAULT 'pending', -- 'pending' | 'approved' | 'rejected'
    batch_id        VARCHAR(64) NOT NULL,    -- groups proposals generated from the same "Go" click
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_gfd_item_proposals_status ON gfd_item_proposals(status, created_at);

-- Reuses iduna.admin, same gate as /admin/gfd-items itself -- no new permission needed.
