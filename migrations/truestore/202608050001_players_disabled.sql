-- First Game Master tool (founder, live: "we will need gamemaster tools for dragonsnshit to
-- start a way to disable accounts and later we will have more gamemaster tools"). players had no
-- status/disabled concept at all -- unlike IDUNA's own `users` table (ACTIVE/PENDING/SUSPENDED),
-- a DragonsNShit account created via email/register (player_email_auth.go) had no way to be
-- turned off short of deleting rows by hand. disabled_at NULL = active (the common case, no
-- column to update on every normal login); non-NULL = disabled, and doubles as an audit
-- timestamp for when a GM took the action, instead of a separate boolean + timestamp pair.

ALTER TABLE players ADD COLUMN disabled_at DATETIME;
