IDUNA VS0 Specification: The Ritual Gate
VS0 is the foundational identity, gating, and game handoff layer for the FARTHQ ecosystem. Its core mission is to serve as the "Ritual Gate"—defining the entry of a mortal actor into an immortal world through identity, oath, name, and entry
OBOBOB.
1. Core Mission and Intent
The purpose of VS0 is to establish a central trust authority that stands between external identity providers (Google) and internal consumers like KIKORYU
. It is defined as a "bureaucratic myth rendered as an interface," where the login process is treated as a ceremony rather than a simple technical step
.
2. Functional Scope
The VS0 release ensures the identity funnel and game handoff works end-to-end
.
Authentication: Integration of Google OAuth as the primary registration and login source
.
Oath Acceptance: Mandatory versioned acceptance of THE_HONOR_CODE using specialized visual semantics
.
Gamertag Lock-in: The permanent reservation of a unique name for the single-shard MMO
.
Device Auth Bridge: A polling mechanism allowing game clients (KIKORYU) to authenticate via a web-based "Confirm Device" flow
.
Gating State Machine: Users progress through a linear state machine: ANON → HONOR → HANDLE → READY
.
3. Visual Language: "Front Office" Aesthetic
The VS0 interface represents the "Front Office" of IDUNA—airy, sacred, and ceremonial
.
Aesthetic Anchor: "Gold leaf on parchment under northern light"
.
Primary Palette:
Backgrounds: Light, warm neutrals like Eggshell (#F4F1EA) or Bone (#EDE7DC)
.
Text: Warm charcoal or umber grey (#3A352E), avoiding pure digital black
.
Typography Stack:
Garamond: Primary identity and ritual text (titles, Honor Code)
.
Georgia: Fallback serif for structural reading
.
Helvetica: Functional system labels and metadata
.
4. Metal Semantics: Gold and Rose Gold
Borders and accents in VS0 are governed by the symbolic meaning of metals
.
Gold (#C6A75E): Represents the System metal. It is used for architecture, structure, and immortality, typically appearing as thin hairline borders and dividers
.
Rose Gold (#B76E79): Represents the Consent metal. It is never decorative and is reserved exclusively for irreversible human agreements
.
Primary Use: The border and text of the "We Agree" button for THE_HONOR_CODE
.
5. THE_HONOR_CODE: The Ritual of Entry
The acceptance of the Honor Code is the primary ritual of VS0
.
The Label: The canonical button label is "We Agree", chosen to reinforce a collective pact and covenant tone rather than a legalistic one
.
Interaction Tone: Pressing the button should feel like engaging a ceremonial mechanism or an "actuator," rather than clicking a standard UI widget
.
Visual Hierarchy: All normal UI elements use neutral greys and gold outlines, while the acceptance action uses rose gold emphasis to signal its unique emotional gravity
.
6. Technical Gating & Device Auth
IDUNA manages the transition from web identity to game world state.
Identity Resolution: Maps external Google subjects to internal stable user IDs
.
The State Machine (/me):
ANON: Authenticated but has not accepted the Honor Code
.
HONOR: Has accepted the Honor Code but lacks a Gamertag
.
HANDLE: Has locked in a Gamertag but has not yet entered the world
.
READY: All gates cleared; ready for game client handoff
.
Device Polling Flow: Includes endpoints for /auth/device/start, /auth/device/poll, and /auth/token/exchange to bridge game clients to the IDUNA web session
.
7. Explicit Non-Scope for VS0
To prevent feature drift, the following are deferred to later versions:
VS1: Email-only auth (magic links/OTP), session revocation, and the "Back Office" (Aunt Sally) administrative console
.
VS2: Poker tournaments, play-money economies, and leaderboards
.
VS3: Play-money stock market games and virtual portfolios
.
