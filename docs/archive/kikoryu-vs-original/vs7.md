VS7 is the Governance & World Authority Layer, representing a major architectural phase where the IDUNA ecosystem transitions from simple systems into a world with formal institutional authority mechanics
. This layer defines who may act on the world, under what authority, with what constraints, and with complete auditability
.
1. Core Concept: Agents vs. Unagents
The fundamental distinction in VS7 is between those with and without system authority
.
Unagent (Default Player State): A normal participant subject to all world rules
. They cannot alter the world state beyond permitted gameplay, spawn entities, or modify persistent structures
.
Agent (Authority-bearing Identity): An identity granted specific system or world authority to perform privileged actions
. Agents are not "admins" in the traditional sense; they are actors with bounded authority operating inside world continuity
.
2. Agent Classes
Roles are defined by rigid sets of capabilities rather than ad-hoc privileges
.
GM_AGENT (Game Master): Possesses world interaction authority
. They can teleport players, resolve stuck states, inspect player data, and spawn or test entities
. They cannot modify license entitlements or access billing
.
MOD_AGENT (Moderator): Holds social and order authority
. They are responsible for enforcing THE_HONOR_CODE, resolving disputes, and muting or suspending players
. They have no power to spawn world entities or modify the economy
.
SYS_AGENT (System): Non-human identities used for system automation, such as scheduled events, migrations, or data corrections
.
3. Governance Principles
All Agents must operate under four non-negotiable principles to prevent arbitrary power or systemic distrust
.
Least Authority: Agents receive only the specific capabilities required for their assigned role
.
Full Auditability: Every privileged action must generate an Event Store entry including the actor's identity, timestamp, action payload, and justification
.
Revocability: Agent status must be easily grantable and instantly revocable, with an emergency revoke mechanism always available
.
Visibility Boundaries: Agents can only see hidden world states if their specific role requires it, ensuring observability does not collapse gameplay integrity
.
4. Backend Tooling (The Governance Console)
The administrative tools for VS7 are modeled after a clerical control room or institutional archive
.
Agent Registry: A tool to list agents, manage roles/capabilities, and set expiration timers
.
Authority Actions Console: A surface for privileged operations like teleporting players or forcing state resyncs
.
Player State Inspector: A read-only interface for introspecting player positions, session states, and entitlement snapshots
.
Governance Log Viewer: A queryable event stream tracking all suspensions, overrides, and world corrections
.
5. Visual Identity: The "Back Office"
The Governance UI is the "Back Office of Immortality"
. It should feel like an institutional control room or operational console rather than a modern SaaS dashboard
.
Aesthetic Tone: The interface must be muted, structured, calm, and archival
.
Geometry: Panels should feel like compartments and rows should feel like ledger entries in a physical catalog or filing system
.
Interaction: Actions should feel like filing, approving, or marking records
. Colorful status indicators and "playful" UI elements are strictly forbidden
.
6. Enforcement and Security
The system architecture ensures that the game server acts as the final policy enforcement point
.
Server-Side Logic: All agent privileges are evaluated on the server; no role logic should exist on the client
.
JWT Claims: While IDUNA-issued tokens include role claims, the server must always verify the current state of truth
.
Event-Driven History: All governance actions emit events such as AgentGranted, AgentRevoked, or AuthorityActionExecuted to maintain historical legitimacy
.
7. VS7 "Done" Definition
A successful VS7 implementation is verified when the agent identity model is operational, every privileged action is fully audited, and revocation of authority works instantly across all system and game server nodes
.
