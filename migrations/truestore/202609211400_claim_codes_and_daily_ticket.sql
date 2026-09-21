-- S508: DEADWEIGHT Itch.io survival launch (founder real-time, 2026-09-21 -- executive-advice
-- pivot: "we are doing a survival launch on Itch.io before we move to Steam"). Two generic
-- additions to the same game-scoped online-services surface game_player_tickets/steam-login
-- already established (202609211200): pre-generated claim codes (sold as Itch.io keys, redeemed
-- in-game for tickets + a Founder cosmetic flag) and a daily free-ticket grant so the matchmaking
-- queue doesn't die between purchases. Both generic across every game.Registry entry, not
-- DEADWEIGHT-specific naming, same convention every other game_* table already follows.

CREATE TABLE IF NOT EXISTS game_claim_codes (
    code             VARCHAR(16) NOT NULL,
    game             VARCHAR(32) NOT NULL,
    tickets          INTEGER     NOT NULL DEFAULT 0,
    founder_flag     INTEGER     NOT NULL DEFAULT 0,
    used_by_player_id CHAR(36),
    used_at          DATETIME,
    created_at       DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (code, game)
);

-- Founder cosmetic flag: a real, permanent player attribute (not per-game -- an Itch supporter
-- is a founder everywhere), so it lives on the shared players table rather than a game-scoped one.
ALTER TABLE players ADD COLUMN is_founder INTEGER NOT NULL DEFAULT 0;

-- Daily-freebie bookkeeping lives alongside the balance it grants into -- see game_online.go's
-- grantDailyFreebieIfDue: an atomic UPSERT keyed on this table's own (player_id, game) primary
-- key, gated by this column being NULL or more than 24h old.
ALTER TABLE game_player_tickets ADD COLUMN last_free_ticket_at DATETIME;
