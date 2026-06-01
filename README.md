This task involves a fundamental pivot for IDUNA, evolving it from a game-specific registration service into a robust Platform IAM (Identity and Access Management) Service for the entire FARTHQ ecosystem
.
The following specification outlines the refactor of IDUNA into the central trust authority, incorporating Role-Based Access Control (RBAC) and Agent Governance
.
Refactor Task: IDUNA Platform IAM & Governance Service
1. Vision & Architectural Pivot
IDUNA is now the authoritative service for Identity, Authentication, Authorization, and Governance
. It sits between external identity providers (like Google) and all FARTHQ consumers (FATBABY, KIKORYU, SECWATCH, etc.)
.
Trust Model: Consumer services never directly trust external OAuth providers. They exclusively trust IDUNA-issued JWTs
.
Aesthetic Alignment: The administrative interface must adhere to the "Back Office" (Aunt Sally) aesthetic: institutional, archival, and clerical
.
2. Core Data Model (True Store)
The relational "True Store" must be updated to support a classic RBAC structure and the new Agent entity
.
Identity & Access
users: Manages human identities. Includes id, email, google_subject, gamertag (now an optional profile attribute), and status (ACTIVE, SUSPENDED, BANNED, PENDING)
.
roles: Defines sets of responsibilities (e.g., SUPER_ADMIN, ANALYST, OPERATOR, PLAYER, AGENT)
.
permissions: Fine-grained capabilities (e.g., fatbaby.read, secwatch.execute, governance.admin)
.
user_roles & role_permissions: Many-to-many join tables to link the RBAC chain.
Agent Governance
Agents are first-class identities with bounded authority
.
agents: Includes id, owner_user_id (the human responsible for the agent), name (e.g., EMILY, SECWATCH), type, and status
.
agent_permissions: Allows for independent authorization of automated or privileged actors
.
3. Authentication & Authorization Flow
Implement the following chain of trust:
Authentication: User authenticates via Google OAuth
.
Identity Resolution: IDUNA maps the google_subject to an internal user_id.
Claims Generation: IDUNA generates a JWT containing:
sub: The internal user ID.
roles: Array of assigned role names.
permissions: Flattened array of all permissions derived from roles.
gamertag: Included if the user has locked one in
.
Downstream Verification: FATBABY or KIKORYU validates the IDUNA signature and enforces access based on the permissions claim
.
4. Event Sourcing Requirements
Every state change must be recorded in the Event Store for full auditability
.
New Identity Events: UserCreated, UserSuspended, UserActivated
.
New RBAC Events: RoleAssigned, RoleRevoked, PermissionGranted, PermissionRevoked.
New Governance Events: AgentCreated, AgentSuspended, AuthorityActionExecuted
.
5. Administrative UI (The "Back Office")
The Admin UI is no longer a "dashboard" but an operational governance infrastructure
.
User Management: Search, suspension/reinstatement, and role assignment
.
Agent Registry: View and manage automated agents and their independent capabilities
.
Audit Viewer: Queryable stream of the Event Store to track every privileged action
.
Visual Direction: Muted tones, structured rows (ledger feel), and Rose Gold accents used only for irreversible consent or critical agreement actions
.
6. Implementation Checklist for Claude Code
[x] Implement MySQL schema for RBAC (users, roles, permissions, agents, and join tables).
[x] Refactor /auth endpoints to handle the transition from Google OAuth to internal JWT issuance.
[x] Create a JWT signer/generator that injects roles and permissions into claims.
[x] Add middleware to IDUNA's own API to enforce internal permissions (e.g., users.write).
[x] Implement the Event Store producers for all identity and governance actions.
[x] Update the /me or /entitlements endpoint to return the new unified identity and capability profile
.
