# Haat

A village market game on NEAR. Gather mangoes, rice, fish and chillies, trade them with four shopkeepers who are agents with their own NEAR accounts, ride out the monsoon, and rescue a broke shop by investing in it. Every item, coin and share is a NEP-245 token in one contract; players never see a wallet, a key or a gas fee.

Status: work in progress. The contract, the web game, the four shopkeeper agents, the day clock and a first market dashboard run on testnet today. Fast-forward, a one-command local sandbox mode and the hosted demo come next. See [DESIGN.md](DESIGN.md) for the full design.

## Layout

```
contract/   Rust market contract (near-sdk 5): NEP-245 core, game rules, pricing.rs
services/   Go: NEAR signing, meta transactions and relayer, market client, shopkeeper agents,
            day clock, and the server-rendered web game (html/template, one tiny inline script)
DESIGN.md   the game loop, economy rules, interfaces and demo script
```

The pricing rules are in one file: [contract/src/pricing.rs](contract/src/pricing.rs).

## Requirements

- Rust with the `wasm32-unknown-unknown` target (the contract pins its toolchain in `contract/rust-toolchain.toml`; rustup installs it on first use)
- [cargo-near](https://github.com/near/cargo-near) 0.22 or later
- Go 1.24 or later
- Network access to NEAR testnet (only for `bootstrap` and `smoke`)

## Build and test

```sh
make contract     # builds contract/target/near/haat_market.wasm
make build        # builds bin/haat
make test-unit    # contract unit tests and Go tests, no network needed
make test         # also runs the sandbox integration tests (downloads near-sandbox on first run)
```

## Run on testnet

```sh
make bootstrap    # creates a root account with the testnet faucet, deploys the market, opens four shops
make smoke        # creates a player, gathers mangoes and sells them to Ramu Kaka through the gas relayer
make status       # prints the day, every shop's prices and purse, and NEAR balances
make serve        # runs the web game on http://localhost:8080 with the day clock and the shopkeepers
```

`serve` needs `HAAT_COOKIE_SECRET` (64 hex characters) in `.env`; `bootstrap` writes one if it is missing. Optional settings: `GROQ_API_KEY` for the shopkeepers' voiced lines, `HAAT_DAY_MINUTES` (default 15), `HAAT_IDLE_MINUTES` (default 30, the clock stops when nobody plays), `HAAT_MAX_NEW_PLAYERS_PER_HOUR` (default 30).

## How login and gas are hidden

There is no sign-up, wallet popup, seed phrase or gas prompt.

1. On "Enter the haat" the server creates a named testnet account such as `anjali-e395.<root>.testnet` with two keys: a play key that can only call the market contract, and a full-access recovery key.
2. Both keys are sealed into an encrypted, tamper-proof cookie (AES-GCM with a server secret). The server keeps no database of keys; it opens the cookie only while signing the player's move.
3. Every move is a NEP-366 meta transaction signed by the play key. The root account relays it and pays the gas, so the player's NEAR balance never changes.
4. The "Your ledger" page has a "Take your account with you" button. It shows the recovery key once, offers it as a near-cli credentials file, and removes it from the cookie, so from then on the account belongs to the player alone.

No free social or email login works without a JavaScript wallet SDK and a dashboard sign-up, so the game uses this auto-created account instead and says so on the ledger page.

## Shopkeepers

Each shopkeeper is its own NEAR account with its own key and NEAR for gas. Its rules are in [services/internal/merchant/persona.go](services/internal/merchant/persona.go): Ramu is cautious, Meena is bold, Bashir reads the almanac and prepares for floods, Lakshmi makes pickle. At each review a shopkeeper signs one transaction with its city trades, any pickle it makes, its new target stock and margin per item, and a one-line note. The contract turns those levers into prices with [contract/src/pricing.rs](contract/src/pricing.rs). If `GROQ_API_KEY` is set, the page shows the note rewritten in the shopkeeper's voice by the open `openai/gpt-oss-20b` model; the model never sets a number.

`bootstrap` needs no login: it creates a fresh `haat-xxxxxx.testnet` account through the public testnet helper and writes the account ids and keys it creates to `.env`, which is gitignored. It is safe to run again; finished steps are skipped.

`smoke` prints explorer links for each transaction and shows that the player's NEAR balance does not change, because the root account pays the gas through NEP-366 meta transactions.

## License

MIT
