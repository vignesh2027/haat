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
	for _, spec := range merchant.Roster {
		acct, err := a.merchantAccount(spec, root.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		k.Agents = append(k.Agents, &merchant.Agent{Spec: spec, Account: acct, Market: mc})
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
	srv.Relayer = near.NewRelayer(root, mc.Contract)
	srv.Voice = merchant.NewVoice(a.env.Get("GROQ_API_KEY"))
	if m := a.env.Get("HAAT_VOICE_MODEL"); m != "" {
		srv.Voice.Model = m
	}
	srv.Explorer = a.env.Or("NEAR_EXPLORER_URL", "https://testnet.nearblocks.io")
	srv.Network = a.env.Or("NEAR_NETWORK", "testnet")
	if n, err := strconv.Atoi(a.env.Get("HAAT_MAX_NEW_PLAYERS_PER_HOUR")); err == nil && n > 0 {
		srv.MaxNewPlayersPerHour = n
	}

	go k.Run(ctx)
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
