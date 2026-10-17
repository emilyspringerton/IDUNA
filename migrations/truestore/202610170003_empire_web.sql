-- EMPIRE web (WOTAN /empire/, founder real-time 2026-10-17: "build the whole vs0 mvp of EMPIRE: ESPORTS WOTAN IDUNA SSO PARENA WASM TS RIGHT INTO WOTAN
-- STATS ALL OF IT AIs"). The per-game identity scope (guest accounts, Claim Account, ssoGamePlayer ride the generic internal/games.Registry machinery),
-- then what the referee (internal/empire) records.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000065', 'empire.play',
   'Guest player token scope: create/resume an EMPIRE account. Nothing else.');

-- One row per match (not per person: two of the AIs play each other in the league). The Elo / win / loss counters WOTAN's ladder reads
-- (game_player_stats and game_matches for game 'empire') are moved in the same transaction by the shared applyGameMatch.
CREATE TABLE IF NOT EXISTS empire_matches (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    mode           TEXT    NOT NULL DEFAULT 'ranked',      -- ranked | league
    seat0_player   TEXT    NOT NULL,
    seat1_player   TEXT    NOT NULL,
    seat0_name     TEXT    NOT NULL DEFAULT '',
    seat1_name     TEXT    NOT NULL DEFAULT '',
    seat0_bot      INTEGER NOT NULL DEFAULT 0,             -- 0 a person, else the AI's number 1..8
    seat1_bot      INTEGER NOT NULL DEFAULT 0,
    seed           INTEGER NOT NULL DEFAULT 0,
    sim_version    INTEGER NOT NULL DEFAULT 0,             -- the rules the match ran on; a replay needs the same
    winner         INTEGER NOT NULL DEFAULT 2,             -- 0 | 1 | 2 draw
    reason         INTEGER NOT NULL DEFAULT 0,             -- 1 king fell | 2 more crowns | 3 first tower in overtime | 4 more tower health | 5 level | 6 resigned
    ticks          INTEGER NOT NULL DEFAULT 0,             -- 20 a second
    crowns0        INTEGER NOT NULL DEFAULT 0,
    crowns1        INTEGER NOT NULL DEFAULT 0,
    tower_hp0      INTEGER NOT NULL DEFAULT 0,
    tower_hp1      INTEGER NOT NULL DEFAULT 0,
    towers0        INTEGER NOT NULL DEFAULT 0,
    towers1        INTEGER NOT NULL DEFAULT 0,
    deck0          INTEGER NOT NULL DEFAULT 0,             -- eight card ids, a nibble each
    deck1          INTEGER NOT NULL DEFAULT 0,
    faction0       INTEGER NOT NULL DEFAULT 0,             -- 0 SYNDICATE | 1 GOETIA | 2 ARCHIVE
    faction1       INTEGER NOT NULL DEFAULT 0,
    stats0         TEXT    NOT NULL DEFAULT '{}',          -- JSON: what the board counted for each seat
    stats1         TEXT    NOT NULL DEFAULT '{}',
    plays0         TEXT    NOT NULL DEFAULT '[]',          -- JSON: plays per card id 0..15
    plays1         TEXT    NOT NULL DEFAULT '[]',
    rating0_before REAL    NOT NULL DEFAULT 0,
    rating0_after  REAL    NOT NULL DEFAULT 0,
    rating1_before REAL    NOT NULL DEFAULT 0,
    rating1_after  REAL    NOT NULL DEFAULT 0,
    counted        INTEGER NOT NULL DEFAULT 0,
    replay         BLOB,                                   -- the file WOTAN's viewer loads (seed, decks, every executed play)
    played_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_empire_m_p0 ON empire_matches (seat0_player, id DESC);
CREATE INDEX IF NOT EXISTS idx_empire_m_p1 ON empire_matches (seat1_player, id DESC);
CREATE INDEX IF NOT EXISTS idx_empire_m_recent ON empire_matches (id DESC);

-- how each card fares: per card, and per player and card. games = matches with the card in the deck, plays = times it was cast.
CREATE TABLE IF NOT EXISTS empire_card_stats (
    card   INTEGER PRIMARY KEY,
    games  INTEGER NOT NULL DEFAULT 0,
    wins   INTEGER NOT NULL DEFAULT 0,
    plays  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS empire_player_cards (
    player_id TEXT    NOT NULL,
    card      INTEGER NOT NULL,
    games     INTEGER NOT NULL DEFAULT 0,
    wins      INTEGER NOT NULL DEFAULT 0,
    plays     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (player_id, card)
);
