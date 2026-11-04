//go:build story

package main

// The story test plays a seeded week on a private local node and checks the
// beats the game promises: the bold shopkeeper goes broke and is rescued, the
// prudent ones never close, and Bashir comes out of the floods ahead.
//
// Run it with `make story` (it builds the contract and downloads near-sandbox
// once). Shopkeepers use their rule books here, so the run is repeatable.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"haat/internal/config"
	"haat/internal/market"
)

func TestStoryWeek(t *testing.T) {
	repo, _ := filepath.Abs("../../..")
	wasm := filepath.Join(repo, "contract/target/near/haat_market.wasm")
	if _, err := os.Stat(wasm); err != nil {
		t.Skip("build the contract first: make contract")
	}
	dir := t.TempDir()
	port := 3200 + rand.Intn(500)
	settings := map[string]string{
		"HAAT_LOCALNET_DIR":      dir,
		"HAAT_LOCALNET_RPC_PORT": strconv.Itoa(port),
		"HAAT_LOCALNET_P2P_PORT": strconv.Itoa(port + 1000),
		"HAAT_WORLD_SEED":        "129", // floods on days 7, 8 and 10
		"HAAT_START_DAY":         "3",
		"HAAT_AGENT_BRAIN":       "rules",
		"HAAT_WASM":              wasm,
	}
	if bin := filepath.Join(repo, ".localnet/bin/near-sandbox"); fileExists(bin) {
		settings["NEAR_SANDBOX_BIN_PATH"] = bin
	}
	for k, v := range settings {
		t.Setenv(k, v)
	}
	env, err := config.Load(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{env: env}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	stop, err := a.startLocalnet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := a.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	k, _, mc, err := a.keeper()
	if err != nil {
		t.Fatal(err)
	}
	start, err := mc.Merchants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k.FastForwardsPerHour = 0
	if err := k.StartFastForward(7); err != nil {
		t.Fatal(err)
	}
	for k.FastForwardState().Running {
		time.Sleep(500 * time.Millisecond)
	}
	if e := k.FastForwardState().Err; e != "" {
		t.Fatalf("fast-forward stopped: %s", e)
	}
	n, _ := mc.ChronicleLen(ctx)
	chron, err := mc.Chronicle(ctx, 0, n)
	if err != nil {
		t.Fatal(err)
	}
	end, err := mc.Merchants(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chron {
		who := ""
		if c.MerchantID != nil {
			who = *c.MerchantID
		}
		t.Logf("day %2d %-14s %s", c.Day, c.Kind, who)
	}

	closed, reopenedAfter := map[string]int{}, map[string]bool{}
	for _, c := range chron {
		if c.MerchantID == nil {
			continue
		}
		key := shopKey(*c.MerchantID)
		switch c.Kind {
		case "shop_closed":
			closed[key]++
		case "shop_reopened":
			if closed[key] > 0 {
				reopenedAfter[key] = true
			}
		}
	}
	if closed["meena"] == 0 {
		t.Error("bold Meena should go broke in the week before the rains")
	}
	if !reopenedAfter["meena"] {
		t.Error("someone should rescue Meena's shop")
	}
	for _, prudent := range []string{"ramu", "bashir", "lakshmi"} {
		if closed[prudent] > 0 {
			t.Errorf("%s should never close: its reserve protects it", prudent)
		}
	}
	purse := func(shops []market.Merchant, key string) int64 {
		for _, m := range shops {
			if shopKey(m.AccountID) == key {
				return int64(m.Kauri)
			}
		}
		return 0
	}
	b0, b1 := purse(start, "bashir"), purse(end, "bashir")
	t.Logf("Bashir's purse: %d at the start of the week, %d after the floods", b0, b1)
	if b1 <= b0 {
		t.Errorf("Bashir should come out of the floods ahead: %d then %d", b0, b1)
	}
	fmt.Println()
}

func shopKey(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '.' {
			return id[:i]
		}
	}
	return id
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
