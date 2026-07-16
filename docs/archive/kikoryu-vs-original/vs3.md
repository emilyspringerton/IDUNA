VS3 is the Play Money Stock Market Game, a non-redeemable paper trading simulation where players manage fictional portfolios and compete on leaderboards
. It is built upon the stable identity and moderation foundations of VS1 and follows the competitive institutions established in VS2
.
1. Core Constraints and Purpose
The primary goal of VS3 is to provide a structured economic simulation within the IDUNA ecosystem
. To maintain legal safety and system integrity, it adheres to several non-negotiable constraints
:
No Real Assets: There are no real money deposits, withdrawals, or redemptions
.
No Real Execution: The game does not integrate with brokerages or execute trades on real markets
.
No Financial Advice: The UI must feature a clear disclaimer: “For entertainment only. Virtual funds. Not investment advice”
.
2. Gameplay Loop
Bankroll: Players start with a fixed virtual bankroll (e.g., $100,000)
.
Execution: Players place buy/sell orders for supported symbols, which fill based on specific price rules
.
Performance: Portfolio values fluctuate with market prices, and players are ranked on leaderboards by metrics like % return
.
Seasons: Competitions run in 2–4 week cycles where all participants start with the same bankroll
. At the end of each season, results are moved into a permanent End-of-season archive
.
3. Market Data and Tradable Universe
Daily Close Strategy: For VS3, the recommended model is a Daily Close Game
. Orders execute at the official close price for the day, and prices are updated once per day
. This reduces infrastructure complexity and ensures fairness
.
Universe Constraints: Initial scope is limited to US equities only or a curated list of top 100 tickers
.
Exclusions: There is no shorting, margin, or options trading in VS3
.
4. System Architecture (IDUNA-Native)
Following the standard IDUNA architectural pattern, VS3 is entirely event-sourced
:
Event Store: An append-only log of every trade and action (e.g., OrderPlaced, OrderFilled)
.
Projectors: These compute current portfolio states and leaderboards from the event stream
.
True Store: The relational projection of current reality, including tables for seasons, accounts (virtual balances), positions, orders, and leaderboards
.
5. "Back Office" Governance (Aunt Sally)
Governance of the market game falls under the "Aunt Sally" administrative layer, where agents manage the institutional integrity of the simulation
. Admin requirements include:
Defining and updating the tradable universe
.
Starting and ending seasons
.
Halting symbols or correcting erroneous price imports
.
Suspending users from competitions for misconduct
.
6. Visual Identity and UI
The interface for VS3 rejects modern "neon-chart" finance app tropes in favor of an archival/institutional aesthetic
.
The Metaphor: The UI should feel like a registry, ledger, or terminal readout made of paper and brass
.
Screens: Minimalist screens include /market (symbol lists), /portfolio (equity and positions), and /season (active leaderboards)
.
Charts: Charts are kept minimal, primarily using tables and simple sparklines to maintain a "Back Office" feel
.
7. "Done" Definition
A successful VS0–VS3 transition is complete when:
Players can join a season and receive virtual chips
.
Market buy/sell orders fill correctly using the daily close rule
.
Portfolios and leaderboards update accurately based on price snapshots
.
Completed seasons are preserved in the Halls of History for archival viewing
.
