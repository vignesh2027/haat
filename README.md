# Haat

A village market game on NEAR. Gather mangoes, rice, fish and chillies, trade them with four shopkeepers who are agents with their own NEAR accounts, ride out the monsoon, and rescue a broke shop by investing in it. Every item, coin and share is a NEP-245 token in one contract; players never see a wallet, a key or a gas fee.

Status: work in progress. The contract, the signing and relaying layer and one full trade on testnet work today. The web game, shopkeeper agents, fast-forward and dashboard are being built next. See [DESIGN.md](DESIGN.md) for the full design.

## Layout

```
contract/   Rust market contract (near-sdk 5): NEP-245 core, game rules, pricing.rs
services/   Go: NEAR signing, meta transactions and relayer, market client, the haat command
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
```

`bootstrap` needs no login: it creates a fresh `haat-xxxxxx.testnet` account through the public testnet helper and writes the account ids and keys it creates to `.env`, which is gitignored. It is safe to run again; finished steps are skipped.

`smoke` prints explorer links for each transaction and shows that the player's NEAR balance does not change, because the root account pays the gas through NEP-366 meta transactions.

## License

MIT
