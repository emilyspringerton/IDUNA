// cmd/gen-claim-codes -- S508/S508d. One-shot CLI that generates a batch of Itch.io-sellable
// claim codes into game_claim_codes (202609211400 migration), for the "Redeem Code" flow founder
// real-time asked for. Format (S508d): 5 blocks of 5 capital-letters-and-numbers, dash-separated
// (e.g. "DF4XT-QMBGT-F7D49-XXXXX-XXXXX"), matching a Windows-product-key convention players
// already know. Prints the plaintext codes to stdout, one per line -- the founder pastes that
// list straight into Itch's own key-upload feature. There is no other way to retrieve a code's
// plaintext later (the table itself is plaintext, unlike an agent secret, since a claim code is
// meant to be handed to a customer, not kept secret server-side) -- redirect stdout to a file if
// the codes need to be kept.
//
// Usage:
//
//	go run ./cmd/gen-claim-codes -game deadweight -n 500 -tickets 10 -founder=true -tier tier_premium > founder_pack_codes.txt
//	go run ./cmd/gen-claim-codes -game deadweight -n 200 -tickets 5 > refill_codes.txt
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"iduna/internal/store"
)

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // capitals + numbers, no 0/O/1/I -- avoids customer transcription errors

// randomCode -- S508d: 5 blocks of 5, dash-separated (25 real characters + 4 dashes = 29 total),
// e.g. "DF4XT-QMBGT-F7D49-XXXXX-XXXXX" -- matches game_online.go's own claimCodeRe exactly.
func randomCode() (string, error) {
	const blocks, blockLen = 5, 5
	buf := make([]byte, blocks*blockLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, v := range buf {
		if i > 0 && i%blockLen == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(alphabet[int(v)%len(alphabet)])
	}
	return b.String(), nil
}

func main() {
	game := flag.String("game", "", "game slug, e.g. deadweight (required)")
	n := flag.Int("n", 500, "how many codes to generate")
	tickets := flag.Int("tickets", 10, "Draft tickets each code grants")
	founder := flag.Bool("founder", false, "also set the redeeming player's is_founder flag")
	tier := flag.String("tier", "", "also set the redeeming player's account_tier (e.g. tier_premium) -- optional, empty = no tier change")
	dbPath := flag.String("db", "", "sqlite db path (default: $SQLITE_PATH or <IDUNA_ROOT>/var/iduna.db)")
	flag.Parse()
	if *game == "" {
		fmt.Fprintln(os.Stderr, "usage: gen-claim-codes -game <slug> [-n 500] [-tickets 10] [-founder=true]")
		os.Exit(1)
	}

	idunaRoot := envOr("IDUNA_ROOT", ".")
	path := *dbPath
	if path == "" {
		path = envOr("SQLITE_PATH", filepath.Join(idunaRoot, "var", "iduna.db"))
	}
	db, err := store.OpenSQLite(path)
	if err != nil {
		log.Fatalf("open sqlite %s: %v", path, err)
	}
	defer db.Close()

	founderFlag := 0
	if *founder {
		founderFlag = 1
	}

	codes := make([]string, 0, *n)
	seen := map[string]bool{}
	for len(codes) < *n {
		c, err := randomCode()
		if err != nil {
			log.Fatalf("generate code: %v", err)
		}
		if seen[c] {
			continue // vanishingly rare at 25 real chars, but never silently under-deliver the requested count
		}
		seen[c] = true
		codes = append(codes, c)
	}

	var tierArg any
	if *tier != "" {
		tierArg = *tier
	}
	tx, err := db.Begin()
	if err != nil {
		log.Fatalf("begin tx: %v", err)
	}
	for _, c := range codes {
		if _, err := tx.Exec(
			`INSERT INTO game_claim_codes (code, game, tickets, founder_flag, tier) VALUES (?,?,?,?,?)`,
			c, *game, *tickets, founderFlag, tierArg); err != nil {
			tx.Rollback()
			log.Fatalf("insert code %s: %v", c, err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}

	for _, c := range codes {
		fmt.Println(c)
	}
	fmt.Fprintf(os.Stderr, "gen-claim-codes: inserted %d codes for game=%s tickets=%d founder=%v tier=%q\n", len(codes), *game, *tickets, *founder, *tier)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
