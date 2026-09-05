-- S241-01: real fix for the found-not-fixed gap ("IDUNA player accounts work across every game,
-- not just the one they were created for" -- founder, setting up Gary's PAPERCRAFT account:
-- "make sure that account only for papercraft"). Nullable, no default: an existing player row
-- (every account created before this migration) keeps game NULL, and every ticket handler
-- treats an absent claim as unscoped -- backward compatible by construction, not by a special
-- case in application code.
ALTER TABLE players ADD COLUMN game VARCHAR(32);
