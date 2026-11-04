// Command haat runs the Haat village market: bootstrap the accounts and
// contract, run the web game and the shopkeeper agents, or run a smoke test.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const usage = `haat: a village market with shopkeeper agents on NEAR

Usage:
  haat [-env FILE] bootstrap   create accounts, deploy the market, open the shops
  haat [-env FILE] smoke       create a player and make one real trade end to end
  haat [-env FILE] status      print the world, the shops and account balances
  haat [-env FILE] serve       run the web game, the day clock and the shopkeeper agents
  haat [-env FILE] reset-market  redeploy the contract and start a fresh world (then run bootstrap)
  haat [-env FILE] localnet    run everything on a local near-sandbox node (add "reset" to wipe it)
  haat [-env FILE] fast-forward  play 7 days back to back from the command line
  haat [-env FILE] review      let every shopkeeper review its shop once
  haat [-env FILE] end-day     settle every shop and move to the next day

Settings come from the environment and the -env file (default .env).
`

func main() {
	envPath := flag.String("env", ".env", "settings file (gitignored)")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := loadApp(*envPath)
	if err != nil {
		fail(err)
	}
	switch cmd := flag.Arg(0); cmd {
	case "bootstrap":
		err = app.bootstrap(ctx)
	case "smoke":
		err = app.smoke(ctx)
	case "status":
		err = app.status(ctx)
	case "reset-market":
		err = app.resetMarket(ctx)
	case "localnet":
		err = app.localnet(ctx, flag.Arg(1))
	case "fast-forward":
		err = app.fastForward(ctx)
	case "serve":
		err = app.serve(ctx)
	case "review":
		err = app.tick(ctx, false)
	case "end-day":
		err = app.tick(ctx, true)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", strings.TrimSpace(err.Error()))
	os.Exit(1)
}
