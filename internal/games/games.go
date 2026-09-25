// Package games is the per-game configuration table for IDUNA's generic online-services routes
// (/api/v1/games/{game}/..., /api/v1/game-checkpoints/{game}/...). S503-06: adding a game with guest
// accounts, match results and a checkpoint registry is one row here (plus a migration granting its
// permissions/agents), not a new handler.
package games

import "os"

// Config describes one game's online-service surface.
type Config struct {
	Slug string
	// PlayPerm is the only permission a guest player token carries.
	PlayPerm string
	// BotPerm lets an M2M bot agent authenticate to the game server (verify) and get a bot player row.
	BotPerm string
	// MatchWritePerm lets the game server report authoritative results.
	MatchWritePerm string
	// CheckpointsWritePerm gates checkpoint upload / match-result on the game-scoped registry.
	CheckpointsWritePerm string
	// CheckpointBlobDir is where this game's registry blobs live on disk.
	CheckpointBlobDir string
	// TicketsWritePerm lets the game server (never a bot) consume a player's ticket balance
	// before allocating a paid match instance (S507, DEADWEIGHT Steam F2P onboarding).
	TicketsWritePerm string
	// SteamAppID gates steam-login for this game: empty means Steam auth is not configured for
	// it (404s, same "not wired up yet" honesty as every other optional integration in this
	// repo -- see game_online.go's steamLogin). Read from env at process start (see games.go's
	// own init()), never hardcoded, since a real Steam App ID doesn't exist until the founder's
	// own Steamworks partner account issues one.
	SteamAppID string
}

// Registry is keyed by slug. Guest accounts and match results are enabled for every entry;
// "brawlpit" is not listed here on purpose: it keeps its historic routes/permission untouched.
var Registry = map[string]Config{
	"deadweight": {
		Slug:                 "deadweight",
		PlayPerm:             "deadweight.play",
		BotPerm:              "deadweight.bot.play",
		MatchWritePerm:       "deadweight.match.write",
		CheckpointsWritePerm: "deadweight.checkpoints.write",
		CheckpointBlobDir:    "./var/deadweight-checkpoints",
		TicketsWritePerm:     "deadweight.tickets.write",
		// DEADWEIGHT_STEAM_APPID: unset until the founder's own Steamworks partner account
		// issues a real App ID (Valve-gated, human-only step -- same class of external
		// dependency as OpenExecutive's own real GCP Vertex AI project gap). steam-login 404s
		// for this game until it's set.
		SteamAppID: os.Getenv("DEADWEIGHT_STEAM_APPID"),
	},
	// Kanban card 123214231: "we need a big_o account creation interface off of iduna". BIG_O
	// itself is NORTHSTAR-only (no server/client code yet, see BIG_O/NORTHSTAR.md) -- only
	// PlayPerm is wired so guest-register/guest-login/guest-upgrade work today; the rest stay
	// empty on purpose (no bot/server exists yet to hold those permissions -- add them, mirroring
	// deadweight's own row, once BIG_O has real server code to consume them).
	"big_o": {
		Slug:     "big_o",
		PlayPerm: "big_o.play",
	},
	// SHANKPIT_OS_NORTHSTAR.md item 4 ("genuinely new work"): BRAWLPIT had no player-facing
	// identity at all before this -- brawlpit.checkpoints.write (202609131400) is a real,
	// separate, already-live M2M-only permission, untouched here. Same "PlayPerm only, no
	// bot/match perms yet" scoping as big_o above -- no server consumes those yet.
	"brawlpit": {
		Slug:     "brawlpit",
		PlayPerm: "brawlpit.play",
	},
	// D2/NORTHSTAR.md ("server-authoritative 1v1"): its own M2M agent identity, never reusing
	// DEADWEIGHT's or ECOWAR's (ECOWAR-BOTS's own precedent) -- dw2_server reports results as
	// D2-SERVER. Renamed from "deadweight_2" (founder real-time: "just call it D2, disambiguate
	// it from DEADWEIGHT, D2 is the official studio name") via
	// 202609251200_rename_deadweight2_to_d2.sql -- the repo itself is also being renamed,
	// DEADWEIGHT_2 -> D2, separately blocked on GitHub token permissions as of this change. No
	// BotPerm/CheckpointsWritePerm/TicketsWritePerm yet: D4 ("a real placeholder bot") is a
	// separate, not-yet-built phase -- same deliberately narrow cut
	// 202609221100_big_o_play_permission.sql's own doc comment named for big_o.
	"d2": {
		Slug:           "d2",
		PlayPerm:       "d2.play",
		MatchWritePerm: "d2.match.write",
	},
}
