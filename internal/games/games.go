// Package games is the per-game configuration table for IDUNA's generic online-services routes
// (/api/v1/games/{game}/..., /api/v1/game-checkpoints/{game}/...). S503-06: adding a game with guest
// accounts, match results and a checkpoint registry is one row here (plus a migration granting its
// permissions/agents), not a new handler.
package games

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
	},
}
