.PHONY: all contract build test test-unit test-contract test-sandbox test-go bootstrap smoke status serve localnet localnet-reset fast-forward deploy clean

WASM := contract/target/near/haat_market.wasm

all: contract build

## Build the market contract wasm with cargo-near.
contract:
	cd contract && cargo near build non-reproducible-wasm

## Build the Go binary to bin/haat.
build:
	cd services && go build -o ../bin/haat ./cmd/haat

## Every test: contract unit tests, sandbox integration tests, Go tests.
test: test-contract test-sandbox test-go

## Fast tests that need no network and no sandbox.
test-unit: test-contract test-go

test-contract:
	cd contract && cargo test --lib

test-sandbox:
	cd contract && cargo test --test sandbox

test-go:
	cd services && go vet ./... && go test ./...

## Create the testnet accounts, deploy the market and open the shops (idempotent).
bootstrap: contract build
	./bin/haat bootstrap

## Create a fresh player and make one real trade through the gas relayer.
smoke: build
	./bin/haat smoke

status: build
	./bin/haat status

## Run the web game, the day clock and the shopkeeper agents.
serve: build
	./bin/haat serve

## Run the whole game on a local NEAR node (near-sandbox): no testnet account, no faucet.
localnet: contract build
	./bin/haat -env .localnet/env localnet

## Wipe the local chain and its accounts.
localnet-reset: build
	./bin/haat -env .localnet/env localnet reset

## Play a week back to back against the running local node and print what happened.
fast-forward: build
	./bin/haat -env .localnet/env fast-forward

## Host the game on Modal (needs the modal CLI and a bootstrapped .env).
deploy:
	cd services && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ../bin/haat-linux-amd64 ./cmd/haat
	modal deploy deploy/modal_app.py

clean:
	rm -rf bin contract/target
