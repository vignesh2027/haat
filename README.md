<p align="center"><img src="services/internal/web/static/logo-512.png" alt="Haat logo" width="120"></p>

# Haat

A village market game on NEAR, for the Games With Real Economies track. Four shopkeepers are agents with their own NEAR accounts; prices move with stock and the monsoon; a bold shop goes broke and you can rescue it for a share of its profits. Every item, coin and share is a NEP-245 token, every move is a real transaction, and players never see a wallet, a key or a gas fee.

**Play now:** https://haat.140-245-16-129.sslip.io (NEAR testnet, one shared village)

**Demo video:** https://youtu.be/fxmxD8VAd-0

Music: "Vadodora Chill Mix" Kevin MacLeod (incompetech.com)<br>
Licensed under Creative Commons: By Attribution 4.0 License<br>
http://creativecommons.org/licenses/by/4.0/

**Run it yourself in one command** (local NEAR node, no account needed): `make localnet`, then open http://localhost:8080

**Three things to try**

1. **Enter, gather, sell.** Tap the mango grove a few times, then hover a price at a stall: the shopkeeper explains it from the same numbers the contract uses. Sell, and watch each extra mango fetch less.
2. **Watch an agent decide.** Open "How Meena Didi decided lately" on her stall: her note, what her rule book suggested, what she chose, and the transaction she signed from her own account.
3. **Fast-forward.** Press "1 day" until bold Meena goes broke (usually a day or two), rescue her with Invest, then "Fast-forward 7 days" into the monsoon floods and watch the market dashboard, the chronicle and your lamp on the leaderboard.

Then press **3D village**: the same square drawn from the chain, with live prices on the stalls, shutters on a broke shop, monsoon rain and a flooded road, and a tap on any stall to trade.

`make story` checks those beats automatically: it plays a seeded week on a private node and asserts that Meena goes broke and is rescued, the prudent shops never close, and Bashir comes out of the floods ahead.

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

Every fresh local village is the same seeded story: it opens on day 3 of summer, the monsoon starts on day 7 and the road floods on days 7, 8 and 10. Bold Meena Didi usually goes broke on day 4 or 5; Kishan, the investor villager, rescues her a day later unless you get there first. Bashir Bhai, who reads the almanac, comes out of the floods ahead.

Shopkeepers think with an open model when `GROQ_API_KEY` is set (in the environment or in `.env`; Groq's free tier, no card). Without it they use their rule books and the game is otherwise identical.

## Tests

```sh
make test-unit   # contract unit tests and Go tests, no network (about 2 minutes cold)
make test        # also deploys the contract to a local near-sandbox, plays real transactions, and runs make story
make story       # plays the seeded week on a private local node and checks the story beats (about 30 s)
```

The contract tests include regression tests for every economic issue found in review: a buy-then-sell round trip can never profit (plus a property test over many shelves), dividends come only from new highs, late investors do not share earlier profit, and a kauri transfer opens or closes a shop at once.

## Run it on NEAR testnet

```sh
make bootstrap    # creates a root account with the public testnet helper, deploys the market, opens the shops, adds villagers
make smoke        # creates a player and sells mangoes to Ramu Kaka through the gas relayer, with explorer links
make serve        # the web game on http://localhost:8080
make status       # the day, every shop's prices and purse, and NEAR balances
```

`bootstrap` needs no login: it asks `helper.nearprotocol.com` for a fresh `haat-xxxxxx.testnet` account and writes the account ids and keys it creates to `.env`, which is gitignored. If the helper is down or rate-limited, use `make localnet` instead. The hosted game runs the same binary (built for linux/arm64) in a small Docker container on an Oracle Cloud Always Free VM, behind Caddy for HTTPS, with the same settings as the Modal app. `make deploy` still hosts it on Modal instead (see [deploy/modal_app.py](deploy/modal_app.py)).

## How it works

```
contract/                 Rust, near-sdk 5: NEP-245 core and metadata, game rules, pricing.rs
services/                 Go, one binary "haat"
  internal/near           keys, borsh, transactions, NEP-366 meta transactions, relayer, JSON-RPC
  internal/market         typed client for the contract
  internal/merchant       shopkeeper personas, rule books, the model "brain", the agent loop
  internal/world          day clock, villagers, fast-forward, storage watchdog
  internal/web            server-rendered pages (html/template), the 3D village, move queue, limits, SVG charts
  cmd/haat                bootstrap, serve, localnet, fast-forward, smoke, status, reset-market
deploy/                   Modal app that runs the binary in one always-on container
```

**Prices.** All pricing is one readable file, [contract/src/pricing.rs](contract/src/pricing.rs), run on chain on every trade. A shop's fair price is the season's city price times a stock factor (2T / (T + s): twice the city price when empty, falling as the stall floods) times a demand factor for asking prices. The shop asks fair plus its margin and bids fair minus its margin, one unit at a time.

**Shopkeepers.** Each is its own NEAR account with its own key and NEAR for gas, and each has a character with hard limits stored in the contract: a margin range, a largest target stock and a cash reserve it never spends (Ramu keeps 1,800 kauri back and a 18 to 25% margin; Bashir keeps 1,250 back and buys from the city only when the almanac shows a flood; bold Meena keeps nothing back). Once a game day, a shopkeeper's open model makes the day's key choice, its target stock and margin per item and the note it signs ([brain.go](services/internal/merchant/brain.go)); a day that looks like one seen before reuses that choice. Its rule book ([persona.go](services/internal/merchant/persona.go)) sizes city cart trades and pickle making, and decides everything when there is no model or the free allowance is used up; the stall says which. The agent clamps every decision to the character limits before signing, and the contract refuses anything outside them.

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
| `HAAT_WORLD_SEED`, `HAAT_START_DAY` | random and 0 (129 and 3 on localnet) | flood calendar seed and the day a new village opens |
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

## Built with

- Contract: Rust, near-sdk 5 and near-contract-standards, built with cargo-near; local runs use near-sandbox.
- Server and NEAR client: Go standard library only.
- The 3D village: Three.js r160 (MIT), vendored in the repo.
- Shopkeepers can think with an open model on Groq when `GROQ_API_KEY` is set; without it they use their rule books.
- Much of the code was written with AI coding assistants (Claude Code) under my direction, during the hackathon window. The contract tests, the Go tests and `make story` check it.

## Also by me for this hackathon

[Mergoshi](https://github.com/vignesh2027/mergoshi), a memecoin that only mints when code ships (memecoins track). The two projects are independent. They share only a small Go NEAR client (base58, keys, transaction signing and RPC) that I copied from one repo to the other.

## License

MIT
