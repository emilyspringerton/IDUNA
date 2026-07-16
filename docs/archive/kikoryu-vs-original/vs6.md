VS6 Specification: KIKORYU Dual/Multibox License (Stripe)
VS6 introduces "permissioned concurrency" to the IDUNA ecosystem, specifically for the KIKORYU MMO
. It provides the mechanism to sell and enforce the right for a single identity to operate multiple concurrent client sessions through a formal licensing system integrated with Stripe
.
1. Goal and Purpose
The objective of VS6 is to allow users to purchase and manage a "Dual/Multibox License" that grants them controlled concurrent sessions
. This system relies on Stripe Checkout for initial purchases and the Stripe Billing Customer Portal for self-service management (cancellations and updates)
.
2. License and Economy Model
To ensure consistent enforcement and revenue, VS6 utilizes a subscription-only model for initial release
.
License Tiers:
DUALBOX: Maximum of 2 concurrent sessions
.
MULTIBOX_3: Maximum of 3 concurrent sessions
.
MULTIBOX_5: Maximum of 5 concurrent sessions
.
Default State: Without a license, the system enforces a base limit of one session per user
.
Grace Window: A 48-to-72 hour grace window is provided upon payment failure before the entitlement is downgraded back to one session
.
3. System Architecture (IDUNA-Native)
The system follows the standard IDUNA pattern of using an Event Store for audit truth and a True Store for operational truth
.
Event Store (Audit Truth)
Every change in license state must be recorded as an event, including:
LicensePurchaseInitiated
StripeCheckoutCompleted
LicenseActivated
LicenseCanceled
LicenseExpired
LicenseRevoked (for administrative or fraud-related actions)
.
True Store (Operational Truth)
Relational tables maintain the current active status of licenses and entitlements
:
licenses: Tracks the current active license tier and status per user
.
stripe_customers: Maps the internal user_id to a unique Stripe Customer ID
.
stripe_subscriptions: Stores subscription status, price IDs, and period end dates
.
license_entitlements: A derived view used for high-speed enforcement of max_concurrent_sessions
.
4. API and Enforcement Mechanism
Endpoints
POST /licenses/checkout: Initiates the purchase flow by creating a Stripe Checkout Session
.
POST /licenses/portal: Generates a link to the Stripe Billing Customer Portal for self-service
.
GET /me/entitlements: A read-only endpoint (used by the game server) to fetch current session limits and status
.
Game Server Enforcement
The KIKORYU game server acts as the final enforcement point at connect-time
:
Validate JWT: Ensure the user identity is legitimate.
Check Entitlements: Query the cached max_concurrent_sessions for the user
.
Active Session Count: Count current sessions with an active heartbeat
.
Reject Excess: If the count exceeds the limit, the connection is rejected with a clear error: ERR_MULTIBOX_LIMIT
.
5. Webhooks as Canonical Truth
To prevent tier spoofing or fraudulent provisioning, license state changes must come exclusively from Stripe webhooks, not browser redirects
. The system must handle events for checkout completion, subscription updates (tier changes), and subscription deletions (revocation)
.
6. Visual Identity and UI
The interface for license management—typically located at /account/license—must adhere to the "Back Office" aesthetic
.
Institutional Tone: The page should feel like an "issued paper," "stamped permit," or "ledger entry" rather than a modern subscription dashboard
.
Material Language: Use gold outlines for structure
. Rose gold is reserved exclusively for irrevocable consent toggles, should the user be required to agree to specific binding terms during the license activation process
.
7. VS6 "Done" Definition
Success is achieved when:
Users can successfully purchase a DUALBOX license via Checkout
.
Webhooks correctly provision entitlements to the True Store
.
The KIKORYU game server correctly enforces session limits
.
Users can manage or cancel their subscriptions via the Stripe Portal
.
The system automatically reverts entitlements to 1 upon expiration or cancellation
.
Agents (VS7) can manually revoke or suspend licenses via the Back Office
.
