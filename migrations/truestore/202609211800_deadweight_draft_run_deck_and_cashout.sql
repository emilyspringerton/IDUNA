-- S510c: real ticket cash-out for Draft Runs (founder real-time -- ready-check for the Itch.io
-- launch plan found this was never built: draftRunLoss's own "run over" branch recorded the final
-- win count to the leaderboard and wiped the run, but never actually granted the player back any
-- tickets, so "Wins 6-9: +1 Ticket ... Wins 25+: +18 Tickets" from the founder's spec had no real
-- backing anywhere). Also adds a place to persist the drafted deck so a boot-time "Draft Hub"
-- resume screen has something real to show without re-querying dw_server's own in-memory deck.

ALTER TABLE game_draft_runs ADD COLUMN deck TEXT;
