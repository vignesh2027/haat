<p align="center"><img src="services/internal/web/static/logo-512.png" alt="Haat logo" width="120"></p>

# Haat

A village market game on NEAR, for the Games With Real Economies track. You gather mangoes, rice, fish and chillies, trade them with four shopkeepers who are agents with their own NEAR accounts, ride out the monsoon, make pickle for Diwali, and rescue a broke shop by investing in it. Every item, coin and share is a NEP-245 token in one contract, every trade is a real transaction, and players never see a wallet, a key or a gas fee.

Hosted demo: https://applemacbook6sep2004--haat-web.us-east.modal.run (NEAR testnet; one shared village)

## What to look at in three minutes

1. Open the hosted demo or run it locally (below) and press **Enter the haat**. No sign-up: an account is made for you.
2. Tap the mango grove a few times, then hover a price at Ramu Kaka's or Lakshmi Amma's stall: the tooltip explains the price from the same numbers the contract uses. Sell, and watch each extra mango fetch less.
3. Open **How Meena Didi decided lately** on a stall: each shopkeeper's latest decisions, what its rule book suggested, what it chose and the transaction it signed.
4. Go to **Market** and press **Fast-forward 7 days**. Villagers trade, shopkeepers decide, floods come, a bold shop goes broke and someone rescues it. Charts, the leaderboard and the chronicle update as each day ends.
5. Open **Your ledger** to see the real account behind your basket and to take its recovery key with you.

## Run it locally (no testnet account needed)

Requirements: Rust (rustup), [cargo-near](https://github.com/near/cargo-near) 0.22 or later, Go 1.22 or later, macOS on Apple silicon or Linux (x86_64 or arm64), and network access to download near-sandbox once.

```sh
make localnet
```

This builds the contract and the Go binary, downloads near-sandbox 2.13.4 into `.localnet/`, starts a local NEAR node, creates the accounts (root, market, four shopkeepers, six villagers), deploys the market, opens the shops and serves the game at http://localhost:8080. The first run takes about two minutes. Stop it with Ctrl+C; run it again to continue the same village.

In a second terminal, while `make localnet` runs:

```sh
make fast-forward     # plays 7 days back to back (about 20 s) and prints the feed, the chronicle and the shops
make localnet-reset   # wipes the local chain; the next make localnet starts a fresh village
```

On a fresh village, Meena Didi usually goes broke on day 2 to 4 and Kishan, the investor villager, rescues her a day later unless you get there first.

Shopkeepers think with an open model when `GROQ_API_KEY` is set (in the environment or in `.env`; Groq's free tier, no card). Without it they use their rule books and the game is otherwise identical.

## Tests

```sh
make test-unit   # contract unit tests and Go tests, no network (about 2 minutes cold)
make test        # also deploys the contract to a local near-sandbox and plays real transactions
```

The contract tests include regression tests for every economic finding of the independent review in [REVIEW.md](REVIEW.md): a buy-then-sell round trip can never profit (plus a property test over many shelves), dividends come only from new highs, late investors do not share earlier profit, and a kauri transfer opens or closes a shop at once.

## Run it on NEAR testnet

```sh
make bootstrap    # creates a root account with the public testnet helper, deploys the market, opens the shops, adds villagers
make smoke        # creates a player and sells mangoes to Ramu Kaka through the gas relayer, with explorer links
make serve        # the web game on http://localhost:8080
make status       # the day, every shop's prices and purse, and NEAR balances
```

`bootstrap` needs no login: it asks `helper.nearprotocol.com` for a fresh `haat-xxxxxx.testnet` account and writes the account ids and keys it creates to `.env`, which is gitignored. If the helper is down or rate-limited, use `make localnet` instead. `make deploy` hosts the game on Modal (see [deploy/modal_app.py](deploy/modal_app.py)).

## How it works

```
contract/                 Rust, near-sdk 5: NEP-245 core and metadata, game rules, pricing.rs
services/                 Go, one binary "haat"
  internal/near           keys, borsh, transactions, NEP-366 meta transactions, relayer, JSON-RPC
  internal/market         typed client for the contract
  internal/merchant       shopkeeper personas, rule books, the model "brain", the agent loop
  internal/world          day clock, villagers, fast-forward, storage watchdog
  internal/web            server-rendered pages (html/template), move queue, limits, SVG charts
  cmd/haat                bootstrap, serve, localnet, fast-forward, smoke, status, reset-market
deploy/                   Modal app that runs the binary in one always-on container
```

**Prices.** All pricing is one readable file, [contract/src/pricing.rs](contract/src/pricing.rs), run on chain on every trade. A shop's fair price is the season's city price times a stock factor (2T / (T + s): twice the city price when empty, falling as the stall floods) times a demand factor for asking prices. The shop asks fair plus its margin and bids fair minus its margin, one unit at a time.

**Shopkeepers.** Each is its own NEAR account with its own key and NEAR for gas. Each review, it reads the chain and decides its levers: target stock and margin per item, city cart trades, pickle to make, and a one-line note. With a model configured, the shopkeeper's model chooses them from its situation, its temperament and its rule book's advice ([brain.go](services/internal/merchant/brain.go)); answers are validated and clamped to the contract's bounds and to what the shop can afford and holds. Without a model, or when the free allowance is used up, the rule book decides ([persona.go](services/internal/merchant/persona.go)), and the stall says which. The shopkeeper signs the result as one transaction; the contract turns the levers into prices.

**Login and gas.** On "Enter the haat" the server creates a named testnet account such as `anjali-e395.<root>.testnet` with two keys: a play key that can only call the market contract, and a full-access recovery key. Both keys are sealed into an encrypted, tamper-proof cookie (AES-GCM); the server keeps no database of keys. Every move is a NEP-366 meta transaction signed by the play key; the root account relays it and pays the gas. "Your ledger" shows the recovery key once and then removes it from the cookie, so the account is the player's alone. There is no free social or email login that works without a JavaScript wallet SDK and a dashboard sign-up, so the game uses this auto-created account and says so on the ledger page.

**Taps feel instant.** A small inline script sends moves in the background. The server checks each move against the player's state minus moves already queued, so obvious failures never reach the relayer, then writes a player's queued moves as one `play()` call about every second. Without JavaScript, the forms still work one move at a time.

**Economy.** Seasons change yields and city prices; monsoon floods stop the city cart; a shop under 200 kauri closes; investing in it mints NEP-245 shares and it reopens; dividends are paid from new highs of the shop's purse through a per-share accumulator, so shares stay tradable with `mt_transfer`. The richest trader on Diwali (day 20 of each 21-day year) leads the leaderboard; each rescue earns a lamp.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | 8080 | web server port |
| `NEAR_NETWORK` | testnet | `testnet` or `localnet` (set by `make localnet`) |
| `NEAR_RPC_URL` | fastnear, near.org and drpc testnet endpoints | JSON-RPC endpoint, or a comma-separated list to fail over between |
| `NEAR_HELPER_URL` | https://helper.nearprotocol.com | testnet account helper used by bootstrap |
| `NEAR_EXPLORER_URL` | https://testnet.nearblocks.io | explorer for links (none on localnet) |
| `GROQ_API_KEY` | none | enables the shopkeepers' model |
| `HAAT_AGENT_BRAIN` | model | `rules` turns the model off |
| `HAAT_AGENT_MODEL` | openai/gpt-oss-20b | open model used by the shopkeepers |
| `HAAT_DAY_MINUTES` | 15 (2 on localnet) | real minutes per game day |
| `HAAT_REVIEW_MINUTES` | 6 | routine shopkeeper reviews while players are around |
| `HAAT_IDLE_MINUTES` | 30 | the clock stops when nobody has played for this long |
| `HAAT_FF_PER_HOUR` | 1 (60 on localnet) | fast-forward runs allowed per hour |
| `HAAT_FF_DAYS` | 7 | days played by `make fast-forward` |
| `HAAT_MAX_NEW_PLAYERS_PER_HOUR` | 60 | global cap on new accounts |
| `HAAT_ACCOUNT_POOL` | 2 | accounts made ahead of time so entering is instant |
| `HAAT_DAILY_TX_BUDGET` | 3000 | relayed transactions per UTC day |
| `HAAT_TRUST_PROXY` | off | set to 1 behind a known proxy to trust forwarded headers |
| `HAAT_MARKET_FUNDING`, `HAAT_MERCHANT_FUNDING` | 4.5, 0.5 | NEAR given to the market and each shopkeeper at bootstrap |
| `HAAT_LOCALNET_DIR`, `HAAT_LOCALNET_RPC_PORT`, `HAAT_LOCALNET_P2P_PORT` | .localnet, 3030, 24568 | local node settings |
| `HAAT_COOKIE_SECRET` | written by bootstrap | 64 hex characters that seal player cookies |

## Trust model

This is a testnet game and the operator has real powers, stated plainly:

- The root account owns the market. It can open shops with any opening purse, give a new account the 400-kauri starter purse, move the calendar on and start a fresh world with `reset_world` (through the market account's key).
- The server signs moves with each player's play key while the player plays; that key can only call the market contract and holds no NEAR beyond the account's storage. The recovery key is shown once on request; an older copy of the cookie (another tab, a saved link) could still carry it.
- Shopkeeper and villager keys live in the operator's `.env`.
- The dividend pool is held by the market account.

## Limits and what is not built

- No NEAR Intents and no social login (see above).
- near-sandbox has no build for Intel Macs; use testnet there.
- The hosted demo shares one village; fast-forward is capped per hour there to keep testnet spend low.

## License

MIT
