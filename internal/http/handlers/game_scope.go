package handlers

// gameClaimMatches implements S241-01's real fix: an already-authenticated player's JWT may
// carry a "game" claim (stamped by PlayerEmailAuthHandler/ShankpitAuthHandler at register/login
// time, from the players.game column). A ticket handler configured with a non-empty want
// rejects a mismatched claim -- but an ABSENT claim (empty string here, since the caller reads
// it via a plain type assertion that zero-values on a missing/non-string key) is treated as
// unscoped and always allowed. This is the deliberate backward-compatibility guarantee named in
// EMILY/BACKLOG.md S241-01: every player account that existed before this column was added, and
// every new account that never set game, keeps working with every ticket handler exactly as it
// did before this fix existed.
func gameClaimMatches(claims map[string]any, want string) bool {
	if want == "" {
		return true
	}
	got, _ := claims["game"].(string)
	return got == "" || got == want
}
