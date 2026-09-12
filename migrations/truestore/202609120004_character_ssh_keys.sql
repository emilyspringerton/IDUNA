-- Real identity binding for GoblinFoxDragon's SSH transport (SSH_TRANSPORT_IDENTITY_SPEC.md §3 /
-- Stage 5, docs2/SSH_TRANSPORT_IDENTITY_NORTHSTAR.md). §3.1: "A character binds to an SSH public
-- key fingerprint... permanently... One WOTAN account may hold multiple fingerprints." Stage 4
-- (§2, shipped same day) already accepts any offered key (trust-on-first-use) for the
-- CONNECTION -- this table is what actually binds one of those keys to a specific, real,
-- permanent character.
--
-- fingerprint is ssh.FingerprintSHA256's own real output format ("SHA256:base64..."), the same
-- string OpenSSH itself shows in `ssh-keygen -lf`/connection logs -- not invented here.
-- public_key is the full authorized_keys-line-format string (ssh.MarshalAuthorizedKey), kept
-- alongside the fingerprint so a real key (not just its hash) is recoverable for §3.2's own
-- "list bound keys" feature without needing the fingerprint alone to be reversible (it isn't).
--
-- Real, deliberate design choice: fingerprint is UNIQUE across the WHOLE table, not scoped to
-- "unique among non-revoked" -- a fingerprint that already belonged to one character can never
-- be reassigned to a different one, even after revocation, matching §3.1's own "permanently"
-- wording literally. A player who revokes then wants the same key back gets it reactivated
-- (revoked_at cleared) for the SAME character it always belonged to -- see
-- IDUNA/internal/http/handlers/ssh_keys.go's own handleBindSSHKey doc comment for the real
-- reactivation-vs-conflict logic this enables.
CREATE TABLE IF NOT EXISTS character_ssh_keys (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    character_id    CHAR(36) NOT NULL,        -- FK -> characters.character_id
    fingerprint     VARCHAR(96) NOT NULL UNIQUE,
    public_key      TEXT     NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at      DATETIME,
    FOREIGN KEY (character_id) REFERENCES characters(character_id)
);
CREATE INDEX IF NOT EXISTS idx_character_ssh_keys_character_id ON character_ssh_keys(character_id);

-- Reads (lookup-by-fingerprint, list-by-character) are open to any authenticated caller --
-- same "not a cheat vector" reasoning handleGetJobLevels already documents. Writes (bind/revoke)
-- are agent-only: see internal/http/handlers/ssh_keys.go's own handleBindSSHKey/
-- handleRevokeSSHKey doc comments -- the "client" here is GoblinFoxDragon's own MUD server
-- acting on a player's behalf after its own claim-flow/key-management command logic, never a
-- player's raw HTTP call.
