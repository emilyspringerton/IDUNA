# VS6 — Dual/Multibox License, Stripe (STATUS: reincarnated-elsewhere)

`supersedes: vs6.md` (archived at `docs/archive/kikoryu-vs-original/vs6.md`)

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS6.*

VS6 specified Stripe-subscription-backed entitlements for concurrent MMO
sessions (DUALBOX/MULTIBOX tiers), with webhooks as the only canonical truth
and game-server enforcement. Multibox itself was never built — but the
billing architecture it demanded shipped for other products, uncredited:

- `internal/http/handlers/subscriptions.go` — `POST /api/v1/subscriptions`
  (provision, `subscriptions.admin`), `GET /api/v1/subscriptions/me`,
  `GET /api/v1/subscriptions/tiers`, and `POST /api/v1/subscriptions/stripe`,
  a **Stripe webhook handler** (`GFD_STRIPE_WEBHOOK_SECRET`) — VS6's
  "webhooks as canonical truth" rule, live.
- `migrations/truestore/202606140002_user_subscriptions.sql` (Emily+ —
  S23-04, done per EMILY/BACKLOG.md despite NORTHSTAR.md still calling it
  pending) and `202606240001_gfd_subscription_tiers.sql` (GFD tiers).
- `auth.Subscription.IsActive()` (`internal/auth/types.go`) — entitlement
  check derived from subscription state.

**Disposition:** superseded. Multibox concurrency licensing is not on the
roadmap (KIKORYU-the-MMO is not the product; tournaments are). If the
tournaments platform ever sells entitlements — entry passes, premium series,
club tiers — it must reuse this exact machinery: Stripe Checkout/Portal,
webhook-only state transitions, event-sourced license lifecycle, derived
entitlement views. VS6's grace-window rule (48–72h on payment failure before
downgrade) carries forward as policy whenever that day comes.
