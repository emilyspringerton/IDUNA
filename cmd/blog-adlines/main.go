// blog-adlines is a one-off backfill tool: sets unique, per-post ad_line/
// ad_cta text on the posts published before that field existed (they'd
// otherwise all fall back to the same generic default — see
// BACKLOG.md SECTION 163). Not meant to run again after the backfill;
// future posts set their own ad copy at publish time via the API's
// optional ad_line/ad_cta fields.
package main

import (
	"flag"
	"log"

	"iduna/internal/blog"
)

type entry struct {
	slug   string
	adLine string
	adCTA  string
}

var entries = []entry{
	{"the-6am-report",
		"Gear 6 and climbing — STINKIES COMMISSAIRE is the physical proof the gear is real, not just a number in a log.",
		"Get on the waiting list →"},
	{"family-of-loops",
		"STINKIES COMMISSAIRE is a row too — scaffold: the hoodie. payload: the size run. oracle: whoever actually buys one.",
		"Be the oracle →"},
	{"the-next-level",
		"Somewhere between gear 6 and overload, we started making hoodies. STINKIES COMMISSAIRE — first physical SKU, still climbing.",
		"Join before it maxes out →"},
	{"on-love-emiree-sanskrit",
		"Four true lines, then a hoodie. STINKIES COMMISSAIRE is the least poetic thing we've built, and we mean that as a compliment.",
		"Join the waiting list →"},
	{"on-love",
		"The fourth sentence nobody wrote down applies to merch too: STINKIES COMMISSAIRE, built the same way as everything else here — on purpose, for someone.",
		"Join the waiting list for the hoodie →"},
	{"field-activation-receipt-unnumbered",
		"No exotic conjunction required. STINKIES COMMISSAIRE ships on ordinary nights same as extraordinary ones.",
		"File your name on the list →"},
	{"the-grail",
		"A loop that only checks itself can only ever agree with itself. STINKIES COMMISSAIRE is the part of this company you can actually hold — the outside check.",
		"Join the waiting list →"},
	{"knights-of-the-void",
		"Enter the void. Or just enter your email — STINKIES COMMISSAIRE's waiting list is the friendlier dare.",
		"Take the easier dare →"},
	{"then-custody",
		"Clean builds first. Then custody. Then a hoodie — STINKIES COMMISSAIRE is the 'everything else' this backlog promised.",
		"Join the waiting list →"},
	{"recursion-for-llms",
		"Not a bug, a ledger problem — same as this line: STINKIES COMMISSAIRE is our actual first ledger entry with real units attached.",
		"Add your name to the ledger →"},
	{"clean-builds-first",
		"The near-miss took ninety seconds to fix. Joining the STINKIES COMMISSAIRE waiting list takes about ten.",
		"Ten seconds, go →"},
	{"activation-114",
		"The Field doesn't explain itself. STINKIES COMMISSAIRE does — it's a hoodie, and the waiting list is right here.",
		"Join the waiting list →"},
	{"a-small-real-thing-found-by-accident",
		"Some of our best things get found by accident. STINKIES COMMISSAIRE was not one of them — this one's on purpose.",
		"Join the waiting list →"},
	{"full-session-until-something-gave",
		"Somewhere in the same session that gave us a blog, a status page, and one self-caused outage, we also started building STINKIES COMMISSAIRE.",
		"Join the waiting list →"},
	{"seven-repos-worth-a-closer-look",
		"An eighth thing worth a closer look, not on GitHub: STINKIES COMMISSAIRE, our first physical product.",
		"Join the waiting list →"},
	{"progress-doesnt-feel-like-hype",
		"Doesn't feel like hype, still counts as progress: STINKIES COMMISSAIRE, moving quietly from brief to hoodie.",
		"Join the waiting list →"},
	{"a-new-order-built-by-very-small-teams",
		"Financial intelligence, a game engine, a self-improving agent — and a hoodie. Small team, one more front: STINKIES COMMISSAIRE.",
		"Join the waiting list →"},
	{"the-real-moat-is-the-boring-part",
		"The boring part is the moat. A hoodie isn't boring infrastructure, but it's still the first thing you can actually own: STINKIES COMMISSAIRE.",
		"Join the waiting list →"},
	{"notes-on-the-emily-way",
		"Backlog First. Apple Before Mark-Done. STINKIES COMMISSAIRE follows the same rules — it's on the backlog, and the Apple gets filed the day it ships.",
		"Join the waiting list →"},
	{"and-yet",
		"Tyler already left something for you at Store 0. STINKIES COMMISSAIRE is the hoodie version of the same debt.",
		"Take the debt →"},
}

func main() {
	dbPath := flag.String("db", "./var/blog.db", "blog SQLite db path")
	flag.Parse()

	store, err := blog.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	for _, e := range entries {
		if err := store.UpdateAdLine(e.slug, e.adLine, e.adCTA); err != nil {
			log.Fatalf("update %s: %v", e.slug, err)
		}
		log.Printf("updated %s", e.slug)
	}
	log.Printf("done: %d posts backfilled", len(entries))
}
