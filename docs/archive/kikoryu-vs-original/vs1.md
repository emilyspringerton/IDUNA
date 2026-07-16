The goal of IDUNA VS1 is to operationalize Identity and Access Management (IAM) to make the system durable for support and moderation while maintaining its status as a central trust authority
. While VS0 focused on the ritual of entry, VS1 is about the "Back Office" of immortality—providing the clerical and administrative infrastructure needed to govern the digital polity
.
1. Functional Scope (IAM & Operational Truth)
VS1 transitions IDUNA from a registration gateway into a functional administrative service.
Authentication Expansion: Introduces an email-only authentication option using magic links or One-Time Passwords (OTP)
.
Session Management: Enables users and administrators to manage active sessions, including the ability to logout of all devices or revoke specific administrative sessions
.
Role-Based Access Control (RBAC): Establishes formal identity tiers: user, moderator, and admin
.
Abuse Controls: Implements rate limiting and abuse prevention for all authentication endpoints to ensure system stability
.
Honor Code Persistence: Enforces a re-acceptance workflow for THE_HONOR_CODE whenever its version is bumped
.
2. The "Back Office" (Aunt Sally) Console
VS1 introduces the first iteration of the administrative interface, conceptually modeled as a "records desk" staffed by a custodian archetype known as Aunt Sally
.
User Management: Administrative tools for searching users (by email, handle, or ID), suspending/unsuspending with recorded reasons, and managing handle reservations or renames
.
Auditability: A queryable Audit Log Viewer that tracks all account changes, suspensions, and identity arbitrations
.
Operational Geometry: The UI must feel mechanical and procedural, with rows that look like ledger entries and panels that feel like physical compartments or drawers
.
3. Visual and Aesthetic Specification
The visual language of VS1 is slightly denser and more "tactile" than the airy front-office of VS0, moving toward a "clerical" tone
.
Typographic Hierarchy:
Garamond: Used for ceremonial identity and ritual text (titles, Honor Code)
.
Helvetica: Used for functional metadata, system UI labels, and administrative status lines in the Back Office
.
The Semantics of Metal:
Gold (#C6A75E): Used for architectural structure, thin borders, and system-level authority
.
Rose Gold (#B76E79): The "consent metal." Reserved exclusively for irreversible human agreements, such as accepting the Honor Code or confirming critical identity changes
.
Material Palette: Backgrounds should use Eggshell (#F4F1EA) or warm parchment tones to avoid the starkness of pure digital white or black
.
4. Technical Constraints & Audit History
Every state transition in VS1 must be recorded in the Event Store to provide a "memory of actions"
.
Non-Destructive History: Administrative actions (like suspensions or handle changes) are never direct database mutations; they are events that produce a recomputable state projection
.
Server Authority: All role-based logic and administrative privileges must be evaluated on the server. Client-side tokens (JWTs) include role claims, but the server always verifies the current "True Store" before executing privileged actions
.
5. VS1 "Done" Definition
Success for VS1 is reached when:
Email-based magic link/OTP authentication is functional
.
The "Back Office" UI allows for basic user search, suspension, and handle management
.
All administrative actions are captured in the immutable Audit Log
.
Session revocation (individual and global) is operational
.
The UI strictly adheres to the "Back Office" aesthetic, avoiding modern SaaS dashboard tropes
.
