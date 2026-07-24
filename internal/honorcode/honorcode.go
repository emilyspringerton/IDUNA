// Package honorcode is the single source of truth for THE_HONOR_CODE text
// and its version/hash. Previously this text existed only as a client-side
// fallback baked into app.js (DECLARATION_TEXT) with no backend source of
// truth to version or hash against — this package closes that gap so
// acceptance can actually be verified server-side (VS0_IDENTITY_GATE.md).
package honorcode

import (
	"crypto/sha256"
	"encoding/hex"
)

// CurrentVersion is bumped whenever CurrentText changes materially enough to
// require re-acceptance from every user (VS0_IDENTITY_GATE.md: "versioned,
// re-acceptance-on-bump covenant"). Version 0 means "never accepted anything"
// (the users table default), so the first real version starts at 1.
const CurrentVersion = 1

// CurrentText is the canonical THE_HONOR_CODE body, verbatim from what the
// ceremony frontend (app.js DECLARATION_TEXT) has been displaying as its
// fallback all along.
const CurrentText = `We know this: progress is not a straight line. Every breakthrough inevitably reshapes society and brings with it unforeseen chains of cause and effect.
Therefore, we do not worship speed alone, deify profit alone, or intoxicate ourselves with novelty alone.
Our path is one of reverence and accountability. We use human wisdom and machine power not in opposition but in complement — not to seize the world, but to support it.

I. Vows (The Four Oaths)
First Oath: Do not devour people.
We will not prey on human weakness. We forbid the design-driven amplification of fear, dependency, shame, loneliness, and anxiety for profit.
Second Oath: Protect the true name.
We will not sell individual dignity piecemeal under the label of "data." We will not disguise invisible surveillance as virtue. We will hold only the minimum necessary knowledge, and how we handle that knowledge will be recorded and made explicit.
Third Oath: Do not make machines into gods.
We will not idolize AI, will not make humans into surplus, and will not depend on "power that cannot be explained." Strength exists alongside responsibility.
Fourth Oath: Restore the world.
Revive industry, warm communities, return order and work to broken foundations. Progress does not abandon cities. Especially — it does not abandon places like Detroit.

II. Barriers (Boundary Design)
We erect boundaries — not walls of exclusion, but kekkai (ritual barriers) to prevent harm from spreading. Narrative is divided into the True (Canon) and the Shadow (Dynamic). The True is not changed. The Shadow may change. But the Shadow must not violate the True.
Advertising belongs to the world, not as an interruption. It does not hold stories hostage. It does not make understanding something you must pay for. Changes are preserved as a ChangeLog so that audiences can see what has been altered.

III. Mandala (The Total Design)
What we build is not a single product. It is a mandala connecting people, cities, factories, logistics, education, culture, safety, and entertainment.
EINHORN_INDUSTRIAL integrates semiconductors, materials, energy, transportation, security, education, and ethical governance — weaving a future in which human skill and dignity do not disappear.
EINHORN_MEDIA does not make storytelling into a trap for consumption. Through transparent technology, it enables responsible enthusiasm.

IV. Mudra (Operational Practice)

Auditable logs
Visible change tracking
Defined boundaries of accountability
Rapid response to fraud and abuse
Postmortem (ritual care for failures) and recurrence prevention

We protect through implementation, not prayer.

V. Ritual Care for Failure (How We Treat Failure)
Concealed failure becomes a grudge-spirit. We give proper rites to failure. We acknowledge it, record it, share it, and fix it. We do not pin it on a single person — we reform the system. We do not distort facts for profit. Ritual care for failure is the continuation of responsibility.

VI. Skillful Means (How We Grow)

Start small and with certainty
Transparency first
Trust as currency
Value continuity over speed


VII. Transference of Merit (Where Profit Goes)
The profit we earn does not accumulate internally alone. It is directed outward — to industry, education, cities, and culture. Victory must not be won in a way that increases the world's debt.

Closing Statement
We declare: progress must walk alongside respect. The greater the power of machines, the thicker the barrier protecting human dignity must become.
Our code is honor. Our honor withstands audit. Our progress leaves no one behind.`

// CurrentSHA256 is the hex-encoded SHA-256 of CurrentText, computed once at
// package init. This is the value clients must echo back on accept, and the
// value stored on users.honor_code_sha once accepted.
var CurrentSHA256 = computeSHA256(CurrentText)

func computeSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
