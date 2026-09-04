-- Kanban board multi-tenancy, Phase 1 (kanban MULTIKANBAN-000: "the kanban is a good primative i
-- want to move it up the abstraction layer i want IDUNA and IDUNA PRO to give the ability to
-- create multiple kanbans"; full real scoping in docs/MULTI_KANBAN_NORTHSTAR.md). Real, decisive
-- finding that scoping pass made: kanban_cards was a single, global, hardcoded table, tied to one
-- specific file (EMILY_BACKLOG_PATH) -- a real structural mismatch with IDUNA_PRO's own tenants,
-- who have no such file. This is the smallest real schema change that fixes it.
--
-- kanban_boards.backlog_path is NULLABLE by design -- board 1 (EINHORN_INDUSTRIAL's own real,
-- existing board) keeps its real git-file-backed sync (backlog.ParseFile, syncNewItemToBacklogGit
-- IfMissing, archiveBacklogItem); a NEW board created with backlog_path = NULL gets genuinely
-- self-contained cards with zero git-sync attempted for it at all -- the real answer this
-- migration commits to for MULTI_KANBAN_NORTHSTAR.md's own open question 1/2 ("does a generic
-- IDUNA_PRO tenant even want git-file-backed sync"): no, only board 1 does, every other board is
-- plain self-contained cards. Real, deliberate NOT-done here, matching that doc's own Phase 2/3:
-- per-board authorization (every board is still gated by the same shared iduna.admin permission
-- today) and a real board-management UI/API.
CREATE TABLE IF NOT EXISTS kanban_boards (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    name         VARCHAR(100) NOT NULL,
    backlog_path VARCHAR(500),  -- NULL = self-contained cards, no git-file sync attempted
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Board 1 = EINHORN_INDUSTRIAL's own real, already-existing board -- every pre-existing
-- kanban_cards row (backfilled to board_id=1 below) keeps behaving exactly as it already does,
-- zero real behavior change for the one board that exists today.
INSERT INTO kanban_boards (id, name, backlog_path)
VALUES (1, 'EINHORN_INDUSTRIAL', '/home/fatbaby/EMILY/BACKLOG.md');

-- board_id, NOT a SQL-enforced foreign key -- matches kanban_cards' own already-established
-- convention (backlog_item_id is deliberately not a foreign key either, per that column's own
-- real doc comment: "don't couple to a moving document's exact structure"). DEFAULT 1 backfills
-- every existing row to the real EINHORN_INDUSTRIAL board with no data migration step needed.
ALTER TABLE kanban_cards ADD COLUMN board_id INTEGER NOT NULL DEFAULT 1;

CREATE INDEX IF NOT EXISTS idx_kanban_cards_board_queue ON kanban_cards(board_id, queue, position);
