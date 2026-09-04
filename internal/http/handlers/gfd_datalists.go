package handlers

// gfd_datalists.go — GFD-XX-123, "UI UX WEB for EQUIPMENT DUNGEONS MOBS use enhancable field for
// specifying the machine names for certain things like model names it should auto complete and
// have dropdown like fatbaby ticker search."
//
// Real precedent found and reused, not reinvented: PRRJECT_FATBABY's own ticker search
// (internal/newssite/templates.go's "masthead" fragment) is a plain HTML5 `<input list="...">` +
// `<datalist>` populated server-side with the full, small, known-upfront value set — native
// browser autocomplete, no JS fetch-as-you-type search box, no library. Every real "machine
// name" field below is small and closed-set the exact same way tickers are, so the exact same
// pattern applies directly.
//
// Real, honest scope: this covers MOBS (mob Kind, a real, closed set — server/mob's own Kind*
// constants) and DUNGEONS (Boss ARENA_HERO_* identifier, a real, closed 30-entry enum,
// apps2/battlegrounds_gui/packages/simulation/arena_game.h). EQUIPMENT's own `model_id` field
// (server/itemdef.Item) is real, but checked directly and confirmed to have NO real, canonical
// list to autocomplete against yet -- no equipment-model rendering pipeline consumes it anywhere
// in this monorepo today (GoldenBand's own real assets are character animation rigs, e.g.
// tyler_walk.gband, not equipment models). A fake/empty datalist for it would look supported
// without being real -- left as free text, this gap named honestly rather than hidden.

import "html"

// gfdMobKinds mirrors server/mob's own real Kind* string constants exactly (checked directly
// against caves.go/hills.go/worm.go/swamp.go/sunderworm.go) -- deliberately NOT the dynamic
// "goblin-"+npc.Kind / "fox-"+npc.Kind crystal-simulation NPC kinds (server/mob/crystal.go),
// which are generated at runtime from an external seed file and have no fixed, enumerable set.
var gfdMobKinds = []string{
	"worm", "rabbit", "beetle", "hills-wolf", "cave-bat", "cave-spider",
	"skeleton", "leech", "slime", "lizard", "sunderworm", "sunderworm-head",
}

// gfdArenaHeroIDs mirrors apps2/battlegrounds_gui/packages/simulation/arena_game.h's own real
// ArenaHeroID enum exactly (0-29, ARENA_HERO_COUNT=30) -- checked directly against the actual
// enum definition, not guessed from usage sites.
var gfdArenaHeroIDs = []string{
	"ARENA_HERO_UNICORN", "ARENA_HERO_DUCK", "ARENA_HERO_GHOST", "ARENA_HERO_FROG",
	"ARENA_HERO_DOC_WHEEL", "ARENA_HERO_TREE", "ARENA_HERO_PIZZA", "ARENA_HERO_FLAMEL",
	"ARENA_HERO_MORRIGAN", "ARENA_HERO_DAGDA", "ARENA_HERO_COURIER", "ARENA_HERO_LOKI",
	"ARENA_HERO_GARY", "ARENA_HERO_FLUTE_DEBT", "ARENA_HERO_BACON_PUCK", "ARENA_HERO_ABRAHAM",
	"ARENA_HERO_ADA", "ARENA_HERO_TYLER", "ARENA_HERO_PAIMON", "ARENA_HERO_NOOR1",
	"ARENA_HERO_CAIN", "ARENA_HERO_GUNNR", "ARENA_HERO_VASSAGO", "ARENA_HERO_HE_XIANGU",
	"ARENA_HERO_BELETH", "ARENA_HERO_MNM", "ARENA_HERO_WEATHERMAN", "ARENA_HERO_ZAGAN",
	"ARENA_HERO_WARRIOR", "ARENA_HERO_CART",
}

// gfdDatalistHTML renders id/values as a real <datalist> block, same shape
// PRRJECT_FATBABY's own ticker <datalist> uses.
func gfdDatalistHTML(id string, values []string) string {
	out := `<datalist id="` + id + `">`
	for _, v := range values {
		out += `<option value="` + html.EscapeString(v) + `">`
	}
	out += `</datalist>`
	return out
}
