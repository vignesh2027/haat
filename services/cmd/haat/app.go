package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"haat/internal/config"
	"haat/internal/market"
	"haat/internal/merchant"
	"haat/internal/near"
	"haat/internal/players"
	"haat/internal/world"
)

type app struct {
	env *config.Env
	rpc *near.Client
}

func loadApp(envPath string) (*app, error) {
	env, err := config.Load(envPath)
	if err != nil {
		return nil, err
	}
	return &app{env: env, rpc: near.NewClient(env.Or("NEAR_RPC_URL", "https://rpc.testnet.fastnear.com"))}, nil
}

func (a *app) explorer(hash string) string {
	if a.env.Get("NEAR_EXPLORER_URL") == "" && a.env.Get("NEAR_NETWORK") == "localnet" {
		return "tx " + hash
	}
	return strings.TrimRight(a.env.Or("NEAR_EXPLORER_URL", "https://testnet.nearblocks.io"), "/") + "/txns/" + hash
}

// root returns the root account signer with its key pool.
func (a *app) root() (*near.Account, error) {
	id, err := a.env.Must("HAAT_ROOT")
	if err != nil {
		return nil, err
	}
	raw, err := a.env.Must("HAAT_ROOT_KEYS")
	if err != nil {
		return nil, err
	}
	var keys []near.KeyPair
	for _, s := range strings.Split(raw, ",") {
		k, err := near.ParseSecretKey(s)
		if err != nil {
			return nil, fmt.Errorf("HAAT_ROOT_KEYS: %w", err)
		}
		keys = append(keys, k)
	}
	return near.NewAccount(a.rpc, id, keys...), nil
}

func (a *app) marketID() (string, error) { return a.env.Must("HAAT_MARKET") }

func (a *app) merchantAccount(spec merchant.Spec, rootID string) (*near.Account, error) {
	raw, err := a.env.Must("HAAT_KEY_" + strings.ToUpper(spec.Key))
	if err != nil {
		return nil, err
	}
	k, err := near.ParseSecretKey(raw)
	if err != nil {
		return nil, err
	}
	return near.NewAccount(a.rpc, spec.Key+"."+rootID, k), nil
}

func (a *app) waitForAccount(ctx context.Context, id string) error {
	for i := 0; i < 30; i++ {
		_, ok, err := a.rpc.AccountBalance(ctx, id)
		if err == nil && ok {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("account %s did not appear", id)
}

func (a *app) exists(ctx context.Context, id string) bool {
	_, ok, err := a.rpc.AccountBalance(ctx, id)
	return err == nil && ok
}

// bootstrap is idempotent: each step is skipped when its result already exists.
func (a *app) bootstrap(ctx context.Context) error {
	// 1. Root account from the testnet faucet, no login needed.
	if a.env.Get("HAAT_ROOT") == "" {
		k, err := near.GenerateKey()
		if err != nil {
			return err
		}
		var b [3]byte
		_, _ = rand.Read(b[:])
		id := "haat-" + hex.EncodeToString(b[:]) + ".testnet"
		helper := a.env.Or("NEAR_HELPER_URL", "https://helper.nearprotocol.com")
		fmt.Println("creating root account", id, "with the testnet faucet")
		if err := near.FaucetCreate(ctx, helper, id, k.Public); err != nil {
			return err
		}
		if err := a.env.Set("HAAT_ROOT", id); err != nil {
			return err
		}
		if err := a.env.Set("HAAT_ROOT_KEYS", k.SecretKeyString()); err != nil {
			return err
		}
		if err := a.waitForAccount(ctx, id); err != nil {
			return err
		}
	}
	root, err := a.root()
	if err != nil {
		return err
	}
	fmt.Println("root:", root.ID)

	// 2. A pool of keys on the root so the relayer can send in parallel.
	if want := 8; len(root.Keys()) < want {
		var adds []near.Action
		var secrets []string
		for _, k := range root.Keys() {
			secrets = append(secrets, k.SecretKeyString())
		}
		for i := len(root.Keys()); i < want; i++ {
			k, err := near.GenerateKey()
			if err != nil {
				return err
			}
			adds = append(adds, near.AddKey{PublicKey: k.Public})
			secrets = append(secrets, k.SecretKeyString())
		}
		out, err := root.Send(ctx, root.ID, adds...)
		if err != nil {
			return fmt.Errorf("adding relayer keys: %w", err)
		}
		fmt.Println("added relayer keys:", a.explorer(out.Hash))
		if err := a.env.Set("HAAT_ROOT_KEYS", strings.Join(secrets, ",")); err != nil {
			return err
		}
		if root, err = a.root(); err != nil {
			return err
		}
	}

	// 3. The market contract on its own sub-account.
	marketID := "market." + root.ID
	if !a.exists(ctx, marketID) {
		wasmPath := a.env.Or("HAAT_WASM", "contract/target/near/haat_market.wasm")
		code, err := os.ReadFile(wasmPath)
		if err != nil {
			return fmt.Errorf("read contract wasm (build it with `make contract`): %w", err)
		}
		k, err := near.GenerateKey()
		if err != nil {
			return err
		}
		seed := a.worldSeed()
		out, err := root.Send(ctx, marketID,
			near.CreateAccount{},
			near.Transfer{Deposit: near.MustNEAR(a.env.Or("HAAT_MARKET_FUNDING", "4.5"))},
			near.AddKey{PublicKey: k.Public},
			near.DeployContract{Code: code},
			market.Init(root.ID, seed),
		)
		if err != nil {
			return fmt.Errorf("deploy market: %w", err)
		}
		fmt.Println("deployed market", marketID, a.explorer(out.Hash))
		if err := a.env.Set("HAAT_MARKET_KEY", k.SecretKeyString()); err != nil {
			return err
		}
		if err := a.env.Set("HAAT_WORLD_SEED", strconv.FormatUint(seed, 10)); err != nil {
			return err
		}
	}
	if err := a.env.Set("HAAT_MARKET", marketID); err != nil {
		return err
	}

	// 4. One account per shopkeeper, each with its own key and NEAR for gas.
	mc := &market.Client{RPC: a.rpc, Contract: marketID}
	existing, err := mc.Merchants(ctx)
	if err != nil {
		return err
	}
	opened := map[string]bool{}
	for _, m := range existing {
		opened[m.AccountID] = true
	}
	var adds []near.Action
	for _, spec := range merchant.Roster {
		id := spec.Key + "." + root.ID
		envKey := "HAAT_KEY_" + strings.ToUpper(spec.Key)
		if !a.exists(ctx, id) {
			k, err := near.GenerateKey()
			if err != nil {
				return err
			}
			out, err := root.Send(ctx, id,
				near.CreateAccount{},
				near.Transfer{Deposit: near.MustNEAR(a.env.Or("HAAT_MERCHANT_FUNDING", "0.5"))},
				near.AddKey{PublicKey: k.Public},
			)
			if err != nil {
				return fmt.Errorf("create %s: %w", id, err)
			}
			if err := a.env.Set(envKey, k.SecretKeyString()); err != nil {
				return err
			}
			fmt.Println("created shopkeeper", id, a.explorer(out.Hash))
		}
		if !opened[id] {
			adds = append(adds, market.AddMerchant(id, spec.Name, spec.Items, spec.OpeningKauri, spec.OpeningStock))
		}
	}
	if len(adds) > 0 {
		out, err := root.Send(ctx, marketID, adds...)
		if err != nil {
			return fmt.Errorf("open shops: %w", err)
		}
		fmt.Println("opened", len(adds), "shops", a.explorer(out.Hash))
		// Each shopkeeper sets its own prices for the first morning.
		if k, _, _, err := a.keeper(); err == nil {
			k.ReviewAll(ctx)
		}
	}
	// 5. Villagers: NPC players that keep the market moving.
	if a.env.Get("HAAT_VILLAGERS") == "" {
		var entries []string
		for _, v := range world.VillagerRoster {
			id := v.Handle + "." + root.ID
			if a.exists(ctx, id) {
				continue
			}
			p, err := players.Create(ctx, root, marketID, v.Handle)
			if err != nil {
				return fmt.Errorf("villager %s: %w", v.Handle, err)
			}
			entries = append(entries, v.Handle+"="+p.Play.SecretKeyString())
			fmt.Println("created villager", p.AccountID)
		}
		if len(entries) > 0 {
			if err := a.env.Set("HAAT_VILLAGERS", strings.Join(entries, ",")); err != nil {
				return err
			}
		}
	}
	if a.env.Get("HAAT_COOKIE_SECRET") == "" {
		var b [32]byte
		_, _ = rand.Read(b[:])
		if err := a.env.Set("HAAT_COOKIE_SECRET", hex.EncodeToString(b[:])); err != nil {
			return err
		}
	}
	fmt.Println("bootstrap complete")
	return a.status(ctx)
}

func (a *app) worldSeed() uint64 {
	if s := a.env.Get("HAAT_WORLD_SEED"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			return v
		}
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:]) >> 12 // keep it inside JSON's safe integer range
}

func formatNEAR(y *big.Int) string {
	if y == nil {
		return "?"
	}
	f := new(big.Float).Quo(new(big.Float).SetInt(y), new(big.Float).SetInt(near.Yocto))
	return f.Text('f', 4)
}

func (a *app) status(ctx context.Context) error {
	marketID, err := a.marketID()
	if err != nil {
		return err
	}
	mc := &market.Client{RPC: a.rpc, Contract: marketID}
	w, err := mc.World(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("day %d, season %s, flood %v, players %d\n", w.Day, w.Season, w.Flood, w.Players)
	shops, err := mc.Merchants(ctx)
	if err != nil {
		return err
	}
	for _, m := range shops {
		bal, _, _ := a.rpc.AccountBalance(ctx, m.AccountID)
		state := "open"
		if !m.Open {
			state = "CLOSED"
		}
		fmt.Printf("  %-14s %-34s %6d kauri  %s NEAR  %s\n", m.Name, m.AccountID, m.Kauri, formatNEAR(bal), state)
		for _, s := range m.Shelves {
			ask := "-"
			if s.Ask != nil {
				ask = strconv.FormatInt(int64(*s.Ask), 10)
			}
			fmt.Printf("      %-7s stock %3d  bid %4d  ask %4s\n", s.TokenID, s.Stock, s.Bid, ask)
		}
	}
	if root := a.env.Get("HAAT_ROOT"); root != "" {
		bal, _, _ := a.rpc.AccountBalance(ctx, root)
		fmt.Printf("root %s: %s NEAR\n", root, formatNEAR(bal))
	}
	return nil
}

// smoke creates a fresh player and makes real trades through the relayer.
func (a *app) smoke(ctx context.Context) error {
	root, err := a.root()
	if err != nil {
		return err
	}
	marketID, err := a.marketID()
	if err != nil {
		return err
	}
	mc := &market.Client{RPC: a.rpc, Contract: marketID}
	relayer := near.NewRelayer(root, marketID)

	p, err := players.Create(ctx, root, marketID, players.RandomName())
	if err != nil {
		return err
	}
	fmt.Println("player:", p.AccountID)
	before, _, _ := a.rpc.AccountBalance(ctx, p.AccountID)

	out, err := players.Act(ctx, relayer, p, market.Play(market.MoveGather("grove"), market.MoveGather("grove"), market.MoveGather("grove")))
	if err != nil {
		return fmt.Errorf("gather: %w", err)
	}
	fmt.Println("gathered at the mango grove (gas paid by relayer):", a.explorer(out.Hash))
	pv, err := mc.Player(ctx, p.AccountID)
	if err != nil {
		return err
	}
	mangoes := pv.Balance("mango")
	fmt.Printf("basket: %d mango, purse: %d kauri, energy left %d\n", mangoes, pv.Balance("kauri"), pv.EnergyLeft)
	if mangoes == 0 {
		return errors.New("no mangoes gathered")
	}

	shop := "ramu." + root.ID
	q, err := mc.Quote(ctx, shop, "mango", "sell", mangoes)
	if err != nil {
		return err
	}
	if q.Total == nil {
		return errors.New("no quote")
	}
	fmt.Printf("Ramu Kaka offers %d kauri for %d mangoes (units %v)\n", *q.Total, mangoes, q.Units)
	out, err = players.Act(ctx, relayer, p, market.Play(market.MoveSell(shop, "mango", mangoes, int64(*q.Total))))
	if err != nil {
		return fmt.Errorf("sell: %w", err)
	}
	fmt.Println("sold to Ramu Kaka:", a.explorer(out.Hash))
	pv, err = mc.Player(ctx, p.AccountID)
	if err != nil {
		return err
	}
	after, _, _ := a.rpc.AccountBalance(ctx, p.AccountID)
	fmt.Printf("basket: %d mango, purse: %d kauri\n", pv.Balance("mango"), pv.Balance("kauri"))
	fmt.Printf("player NEAR balance before %s, after %s (unchanged: the relayer paid the gas)\n", formatNEAR(before), formatNEAR(after))
	return nil
}

// villagers loads the NPC players written by bootstrap.
func (a *app) villagers(rootID string) ([]*world.Villager, error) {
	var out []*world.Villager
	raw := a.env.Get("HAAT_VILLAGERS")
	if raw == "" {
		return nil, nil
	}
	habits := map[string]string{}
	for _, v := range world.VillagerRoster {
		habits[v.Handle] = v.Habit
	}
	for _, entry := range strings.Split(raw, ",") {
		handle, secret, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		k, err := near.ParseSecretKey(secret)
		if err != nil {
			return nil, fmt.Errorf("HAAT_VILLAGERS: %w", err)
		}
		id := handle + "." + rootID
		out = append(out, &world.Villager{
			Player: &players.Player{AccountID: id, Name: players.DisplayName(id), Play: k},
			Habit:  habits[handle],
		})
	}
	return out, nil
}
