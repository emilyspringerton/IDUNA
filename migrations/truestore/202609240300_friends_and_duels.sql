-- S536: friends and friendly-challenge (duel) primitives for the per-game online-services layer
-- (game_online.go). Founder real-time, 2026-09-24: "add iduna online accounts / add social
-- features / profiles / friends / friendly challenges (duels) / for DEADWEIGHT / WOTAN".
-- Scoped per-game (game column), matching every other table this handler already owns
-- (game_player_stats, game_tickets, game_signup_log) -- a friend/duel relationship exists
-- between two players of the SAME game, using the exact per-game player_id identity the
-- guest-register/login flow already mints (players.game, see players_game_scope.sql).
--
-- Friendship has no separate "friendships" table: an accepted friend_requests row IS the
-- friendship (a friends list is a query over status='accepted', either direction). One source
-- of truth, no dual-write/dual-delete invariant to maintain.

CREATE TABLE IF NOT EXISTS friend_requests (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    game             VARCHAR(32)  NOT NULL,
    requester_id     CHAR(36)     NOT NULL,
    recipient_id     CHAR(36)     NOT NULL,
    status           VARCHAR(16)  NOT NULL DEFAULT 'pending', -- pending, accepted, declined, canceled
    created_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    responded_at     DATETIME,
    UNIQUE(game, requester_id, recipient_id)
);
CREATE INDEX IF NOT EXISTS idx_friend_requests_recipient ON friend_requests(game, recipient_id, status);
CREATE INDEX IF NOT EXISTS idx_friend_requests_requester ON friend_requests(game, requester_id, status);

-- Friendly challenges. V0 is the invite lifecycle only (pending/accepted/declined/canceled) --
-- turning an accepted duel into a live match instance is real, named, deferred follow-up work
-- (needs a real per-game match-start mechanism, e.g. DEADWEIGHT's ticket/queue system), not
-- silently punted -- see DEADWEIGHT/NORTHSTAR.md's own social-features section.
CREATE TABLE IF NOT EXISTS duel_challenges (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    game             VARCHAR(32)  NOT NULL,
    challenger_id    CHAR(36)     NOT NULL,
    challenged_id    CHAR(36)     NOT NULL,
    status           VARCHAR(16)  NOT NULL DEFAULT 'pending', -- pending, accepted, declined, canceled
    created_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    responded_at     DATETIME
);
CREATE INDEX IF NOT EXISTS idx_duel_challenges_challenged ON duel_challenges(game, challenged_id, status);
CREATE INDEX IF NOT EXISTS idx_duel_challenges_challenger ON duel_challenges(game, challenger_id, status);
