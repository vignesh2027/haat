package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"haat/internal/market"
	"haat/internal/merchant"
	"haat/internal/near"
	"haat/internal/web"
	"haat/internal/world"
)

func (a *app) minutes(key string, def float64) time.Duration {
	v, err := strconv.ParseFloat(a.env.Or(key, ""), 64)
	if err != nil || v <= 0 {
		v = def
	}
	return time.Duration(v * float64(time.Minute))
}

// keeper wires the root account, the market and the four shopkeeper agents.
func (a *app) keeper() (*world.Keeper, *near.Account, *market.Client, error) {
	root, err := a.root()
	if err != nil {
		return nil, nil, nil, err
	}
	marketID, err := a.marketID()
	if err != nil {
		return nil, nil, nil, err
	}
	mc := &market.Client{RPC: a.rpc, Contract: marketID}
	k := &world.Keeper{
		Root: root, Market: mc,
		DayLength:   a.minutes("HAAT_DAY_MINUTES", 15),
		ReviewEvery: a.minutes("HAAT_REVIEW_MINUTES", 6),
		IdleAfter:   a.minutes("HAAT_IDLE_MINUTES", 30),
	}
	k.Relayer = near.NewRelayer(root, marketID)
	if k.Villagers, err = a.villagers(root.ID); err != nil {
		return nil, nil, nil, err
	}
	k.FastForwardsPerHour = 1
	if n, err := strconv.Atoi(a.env.Get("HAAT_FF_PER_HOUR")); err == nil && n >= 0 {
		k.FastForwardsPerHour = n
	}
	for _, spec := range merchant.Roster {
		acct, err := a.merchantAccount(spec, root.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		k.Agents = append(k.Agents, &merchant.Agent{Spec: spec, Account: acct, Market: mc, Brain: a.brain()})
	}
	return k, root, mc, nil
}

func (a *app) serve(ctx context.Context) error {
	k, root, mc, err := a.keeper()
	if err != nil {
		return err
	}
	secret, err := a.env.Must("HAAT_COOKIE_SECRET")
	if err != nil {
		return err
	}
	srv, err := web.New(secret)
	if err != nil {
		return err
	}
	srv.Root, srv.Market, srv.Keeper = root, mc, k
	srv.Relayer = k.Relayer
	srv.Voice = merchant.NewVoice(a.env.Get("GROQ_API_KEY"))
	if a.brain().Enabled() {
		srv.Voice = nil // shopkeepers write their own notes with the model
	}
	if m := a.env.Get("HAAT_VOICE_MODEL"); m != "" {
		srv.Voice.Model = m
	}
	srv.Network = a.env.Or("NEAR_NETWORK", "testnet")
	srv.Explorer = a.env.Or("NEAR_EXPLORER_URL", "https://testnet.nearblocks.io")
	if srv.Network == "localnet" {
		srv.Explorer = a.env.Get("NEAR_EXPLORER_URL") // no public explorer for a local node
	}
	if n, err := strconv.Atoi(a.env.Get("HAAT_MAX_NEW_PLAYERS_PER_HOUR")); err == nil && n > 0 {
		srv.MaxNewPlayersPerHour = n
	}

	srv.TrustProxy = a.env.Get("HAAT_TRUST_PROXY") == "1"
	srv.PoolSize = 2
	if n, err := strconv.Atoi(a.env.Get("HAAT_ACCOUNT_POOL")); err == nil && n >= 0 {
		srv.PoolSize = n
	}
	if n, err := strconv.Atoi(a.env.Get("HAAT_DAILY_TX_BUDGET")); err == nil && n >= 0 {
		srv.SetDailyBudget(n)
	}
	srv.Start(ctx)
	go k.Run(ctx)
	go k.WatchStorage(ctx)
	addr := ":" + a.env.Or("PORT", "8080")
	hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shut)
	}()
	log.Printf("haat listening on %s (market %s, day length %s, voice %v)", addr, mc.Contract, k.DayLength, srv.Voice.Enabled())
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// tick runs one shopkeeper review, or ends the day with -day.
func (a *app) tick(ctx context.Context, endDay bool) error {
	k, _, _, err := a.keeper()
	if err != nil {
		return err
	}
	if endDay {
		if err := k.EndDay(ctx); err != nil {
			return err
		}
	} else {
		k.ReviewAll(ctx)
	}
	for _, it := range k.Feed(20) {
		mark := ""
		if it.Failed {
			mark = " (failed)"
		}
		fmt.Printf("%s: %s%s %s\n", it.Who, it.Text, mark, a.explorer(it.TxHash))
	}
	return nil
}

// fastForward plays a week from the command line and prints what happened.
func (a *app) fastForward(ctx context.Context) error {
	k, _, mc, err := a.keeper()
	if err != nil {
		return err
	}
	days := 7
	if n, err := strconv.Atoi(a.env.Get("HAAT_FF_DAYS")); err == nil && n > 0 {
		days = n
	}
	k.FastForwardsPerHour = 0
	start := time.Now()
	if err := k.StartFastForward(days); err != nil {
		return err
	}
	for k.FastForwardState().Running {
		time.Sleep(time.Second)
	}
	st := k.FastForwardState()
	for i := len(k.Feed(500)) - 1; i >= 0; i-- {
		it := k.Feed(500)[i]
		fmt.Printf("day %2d  %-13s %s\n", it.Day, it.Who, it.Text)
	}
	if st.Err != "" {
		return fmt.Errorf("fast-forward stopped: %s", st.Err)
	}
	fmt.Printf("\n%d days in %s\n", days, time.Since(start).Round(time.Second))
	for _, ag := range k.Agents {
		for _, t := range ag.Thoughts(20) {
			fmt.Printf("%-13s %-6s %s\n", ag.Spec.Name, t.Source, t.Note)
		}
	}
	n, _ := mc.ChronicleLen(ctx)
	entries, _ := mc.Chronicle(ctx, max(n-30, 0), 30)
	for _, e := range entries {
		who := ""
		if e.MerchantID != nil {
			who = *e.MerchantID
		}
		fmt.Printf("chronicle day %2d %-14s %s\n", e.Day, e.Kind, who)
	}
	return a.status(ctx)
}

var sharedBrain *merchant.Brain

// brain is the shopkeepers' shared model client (one rate budget for all
// four). HAAT_AGENT_BRAIN=rules turns it off.
func (a *app) brain() *merchant.Brain {
	if sharedBrain == nil {
		key := a.env.Get("GROQ_API_KEY")
		if a.env.Get("HAAT_AGENT_BRAIN") == "rules" {
			key = ""
		}
		sharedBrain = merchant.NewBrain(key, a.env.Get("HAAT_AGENT_MODEL"))
	}
	return sharedBrain
}
