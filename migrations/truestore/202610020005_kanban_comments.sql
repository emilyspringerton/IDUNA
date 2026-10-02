-- Comments on kanban cards (founder real-time, 2026-10-02: "add comments to kanban cards so you can leave
-- your questions as comments and a kanban user can leave replies it should be tracked by login").
-- author_sub is the JWT subject of whoever posted (a human's login or an agent's identity); author_name
-- is the display name resolved at post time (agent_name, else email, else sub) so history stays readable
-- if the identity is later renamed.
CREATE TABLE IF NOT EXISTS kanban_comments (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    card_id     INTEGER  NOT NULL,
    author_sub  VARCHAR(128) NOT NULL,
    author_name VARCHAR(200) NOT NULL,
    body        TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_kanban_comments_card ON kanban_comments(card_id, id);
