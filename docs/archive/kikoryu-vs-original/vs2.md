VS2 is the Competitive Institutions layer of the IDUNA civilization stack, specifically introducing No-Limit Texas Hold’em (NLHE) tournaments built upon the stable identity foundations of VS0 and the moderation controls of VS1
. Its mission is to provide structured competitive play using fictional value units within an institutional framework
.
1. Hard Constraints & Economy
To maintain legal safety and system integrity, VS2 operates under a strictly closed, non-redeemable economy
:
No Real Assets: There are no deposits, withdrawals, or secondary markets; chips have zero cash value
.
Tournament-Isolated Chips (Model A): This is the preferred VS0/VS2 model where chips exist only inside a specific tournament instance
. Every player starts with an equal stack (e.g., 10,000), preventing economy exploits, inflation, or "chip farming"
.
Terminology: The system avoids gambling language like "betting" or "wagering" in favor of institutional terms: Chips, Stack, Tournament Entry, Blind Levels, and Standings
.
2. Tournament Lifecycle & State Machine
The backend and UI are driven by a rigid lifecycle to ensure consistency across the single-shard environment
:
CREATED: Tournament exists in the registry.
REGISTERING: Players join; the system displays entrants and start timers
.
STARTING: Entries are locked, players are seated, and stacks are initialized
.
IN_PROGRESS: Handles blind progression, eliminations, and automatic table balancing
.
FINAL_TABLE: Visual emphasis shifts to the concluding participants
.
COMPLETE: Standings are finalized and results are committed to the Event Store
.
3. Technical Requirements
Server Authority: Cards, RNG, dealing, and betting rules must be entirely server-authoritative
. Clients are for rendering and input only to prevent manipulation
.
Auditability: Deterministic hand histories can be stored as events to allow for future replays or administrative review
.
Abuse Prevention: The system must enforce a single active seat per account and rate-limit joins to prevent reconnect exploits
.
4. Identity and Scoreboard Integration
Identity: A user’s Gamertag serves as their permanent table identity
.
VS10 Synergy: Match history and final placements are projected into VS10 Scoreboards, which serve as the "Recognized Narrative of Reality" for the world
. These projections provide institutional legitimacy to a player's historical performance
.
5. Aesthetic & Visual Specification
The UI rejects "Vegas clichés" and "casino vibes" in favor of the "Back Office" (Aunt Sally) aesthetic
.
Visual Tone: It should feel like an organized competition or a structured institution—quietly serious and archival
.
Materials: Use Gold (#C6A75E) for structural geometry (architecture) and Rose Gold (#B76E79) for irreversible "consent metal" moments, such as the final confirmation to register for a tournament
.
UI Elements: Displays should be minimal, showing only the tournament name, blind level, stack size, and placement in a registry-driven list
. Avoid flashy animations or "slot-machine energy"
.
6. VS2 "Done" Definition
Success for VS2 is verified when:
Players can register and login via IDUNA and join a tournament
.
The tournament engine handles the full lifecycle (Registering → Complete) automatically
.
The server correctly enforces all NLHE rules and table balancing
.
Standings are accurately recorded in the Event Store and viewable via the Scoreboard
.
The UI remains strictly consistent with the institutional/archival aesthetic
.
