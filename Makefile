.PHONY: all contract build test test-unit test-contract test-sandbox test-go bootstrap smoke status clean

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

clean:
	rm -rf bin contract/target
