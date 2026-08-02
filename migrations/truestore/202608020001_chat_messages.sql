-- Chat relay between GoblinFoxDragon's apps2/mud (telnet, real say/yell/guild chat) and
-- REDGARDEN's Battlegrounds GUI client (apps2/battlegrounds_gui) -- two separate processes and
-- protocols with no shared channel of their own. IDUNA is the one thing both sides already
-- authenticate against, so it's the relay: either side POSTs a message here, either side polls
-- for new ones since their own cursor. Deliberately no identity linkage to players/characters --
-- apps2/mud's own telnet identity is anonymous/name-keyed (see mudPlayerIDFor's own doc comment
-- in apps2/mud/main.go), not unified with a real IDUNA player_id, so sender_name is a display
-- string, not a foreign key. Any authenticated caller (any valid JWT) may post or poll.
CREATE TABLE IF NOT EXISTS chat_messages (
    id            INTEGER  PRIMARY KEY AUTOINCREMENT,
    channel       VARCHAR(16) NOT NULL,   -- 'say' | 'yell' | 'guild' | 'battlegrounds'
    sender_name   VARCHAR(64) NOT NULL,
    sender_source VARCHAR(16) NOT NULL,   -- 'mud' | 'battlegrounds'
    body          VARCHAR(512) NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chat_messages_id ON chat_messages(id);
