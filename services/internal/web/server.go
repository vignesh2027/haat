// Package web serves the game as server-rendered HTML. Forms post actions,
// the server signs them with the player's play key and the relayer pays the
// gas, then the page reloads. The browser runs only a few lines of inline JS.
package web

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"haat/internal/market"
	"haat/internal/merchant"
	"haat/internal/near"
	"haat/internal/players"
	"haat/internal/world"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Server is the web game.
type Server struct {
	Root     *near.Account
	Relayer  *near.Relayer
	Market   *market.Client
	Keeper   *world.Keeper
	Voice    *merchant.Voice
	Explorer string // e.g. https://testnet.nearblocks.io
	Network  string // testnet or localnet

	// MaxNewPlayersPerHour limits account creation so the demo cannot be drained.
	MaxNewPlayersPerHour int
	// MinRootNEAR stops new accounts when the root balance falls below it.
	MinRootNEAR *big.Int

	sealer *Sealer
	pages  map[string]*template.Template

	mu       sync.Mutex
	created  []time.Time
	cache    *snapshot
	cacheAt  time.Time
	cacheTTL time.Duration
}

type snapshot struct {
	World     market.World
	Merchants []market.Merchant
	Chronicle []market.ChronicleEntry
}

// New builds a server. cookieSecret is 64 hex characters.
func New(cookieSecret string) (*Server, error) {
	sealer, err := NewSealer(cookieSecret)
	if err != nil {
		return nil, err
	}
	s := &Server{sealer: sealer, cacheTTL: 2 * time.Second, MaxNewPlayersPerHour: 30, MinRootNEAR: near.MustNEAR("0.5")}
	if err := s.parse(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) parse() error {
	s.pages = map[string]*template.Template{}
	for _, page := range []string{"welcome", "village", "ledger", "market"} {
		t, err := template.New("layout.html").Funcs(funcs(s)).ParseFS(assets, "templates/layout.html", "templates/"+page+".html")
		if err != nil {
			return fmt.Errorf("template %s: %w", page, err)
		}
		s.pages[page] = t
	}
	return nil
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(http.FileServerFS(static), 3600)))
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /start", s.start)
	mux.HandleFunc("POST /act", s.act)
	mux.HandleFunc("GET /ledger", s.ledger)
	mux.HandleFunc("POST /ledger/reveal", s.reveal)
	mux.HandleFunc("POST /ledger/forget", s.forget)
	mux.HandleFunc("GET /market", s.marketPage)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	return securityHeaders(mux)
}

func cacheFor(h http.Handler, secs int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(secs))
		h.ServeHTTP(w, r)
	})
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) snapshot(ctx context.Context, fresh bool) (*snapshot, error) {
	s.mu.Lock()
	if !fresh && s.cache != nil && time.Since(s.cacheAt) < s.cacheTTL {
		c := s.cache
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()
	var snap snapshot
	var wErr, mErr, cErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); snap.World, wErr = s.Market.World(ctx) }()
	go func() { defer wg.Done(); snap.Merchants, mErr = s.Market.Merchants(ctx) }()
	go func() {
		defer wg.Done()
		n, err := s.Market.ChronicleLen(ctx)
		if err != nil {
			cErr = err
			return
		}
		snap.Chronicle, cErr = s.Market.Chronicle(ctx, max(n-12, 0), 12)
	}()
	wg.Wait()
	if err := errors.Join(wErr, mErr, cErr); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache, s.cacheAt = &snap, time.Now()
	s.mu.Unlock()
	return &snap, nil
}

func (s *Server) invalidate() {
	s.mu.Lock()
	s.cache = nil
	s.mu.Unlock()
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	var b strings.Builder
	if err := s.pages[page].ExecuteTemplate(&b, "layout.html", data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "The page could not be drawn. Please try again.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	sess, ok := s.readSession(r)
	data := s.basePage(snap, takeFlash(w, r))
	if !ok {
		data.Page = "welcome"
		data.Title = "Haat, a village market"
		s.render(w, "welcome", data)
		return
	}
	s.Keeper.Touch()
	pv, err := s.Market.Player(ctx, sess.Account)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	data.Page = "village"
	data.Title = "The haat, day " + strconv.Itoa(snap.World.Day)
	data.Player = buildPlayer(sess, pv, snap)
	data.Stalls = s.buildStalls(snap, &pv)
	s.render(w, "village", data)
}

func (s *Server) unavailable(w http.ResponseWriter, err error) {
	log.Printf("chain read failed: %v", err)
	w.Header().Set("Retry-After", "5")
	http.Error(w, "The village ledger is not answering right now. Please refresh in a few seconds.", http.StatusServiceUnavailable)
}

// start creates a player account on the first visit.
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.readSession(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if !s.allowNewPlayer(ctx) {
		setFlash(w, "err", "The haat is very busy this hour. Please come back a little later.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	p, err := players.Create(ctx, s.Root, s.Market.Contract, players.RandomName())
	if err != nil {
		log.Printf("create player: %v", err)
		setFlash(w, "err", "Could not open your village ledger. Please try again.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	sess := Session{Account: p.AccountID, Play: p.Play.SecretKeyString(), Recovery: p.Recovery.SecretKeyString()}
	if err := s.writeSession(w, r, sess); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	s.Keeper.Touch()
	s.Keeper.Record(world.FeedItem{Who: p.Name, Text: "arrived at the haat with 400 kauri"})
	s.invalidate()
	setFlash(w, "ok", fmt.Sprintf("Welcome, %s. Here are 400 kauri to start. Gather something and sell it at a stall.", p.Name))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) allowNewPlayer(ctx context.Context) bool {
	s.mu.Lock()
	cut := time.Now().Add(-time.Hour)
	kept := s.created[:0]
	for _, t := range s.created {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.created = kept
	if len(s.created) >= s.MaxNewPlayersPerHour {
		s.mu.Unlock()
		return false
	}
	s.created = append(s.created, time.Now())
	s.mu.Unlock()
	if bal, ok, err := s.Root.Client().AccountBalance(ctx, s.Root.ID); err == nil && ok && bal.Cmp(s.MinRootNEAR) < 0 {
		log.Printf("root balance low, refusing new players")
		return false
	}
	return true
}

// act runs one player action through the relayer.
func (s *Server) act(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.readSession(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	s.Keeper.Touch()
	res, err := s.perform(ctx, sess, r.PostForm)
	if err != nil {
		setFlash(w, "err", friendlyError(err))
	} else {
		setFlash(w, "ok", res.message)
		if res.feed != "" {
			s.Keeper.Record(world.FeedItem{Who: players.DisplayName(sess.Account), Text: res.feed, TxHash: res.tx})
		}
	}
	s.invalidate()
	http.Redirect(w, r, "/#"+anchorFor(r.PostForm), http.StatusSeeOther)
}

func anchorFor(f map[string][]string) string {
	if m := first(f, "merchant"); m != "" {
		head, _, _ := strings.Cut(m, ".")
		return "stall-" + head
	}
	return "top"
}

func first(f map[string][]string, k string) string {
	if v := f[k]; len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

// result is what the player is told, and what the village feed says about it.
type result struct {
	message, feed, tx string
}

func (s *Server) perform(ctx context.Context, sess Session, f map[string][]string) (result, error) {
	key, err := sess.PlayKey()
	if err != nil {
		return result{}, err
	}
	kind := first(f, "kind")
	merchantID := first(f, "merchant")
	token := first(f, "token")
	amount, _ := strconv.ParseInt(first(f, "amount"), 10, 64)
	shopName := s.shopName(ctx, merchantID)

	var mv market.Move
	var describe func(v int64) string
	feed := func(v int64) string { return "" }
	switch kind {
	case "gather":
		place := first(f, "place")
		item := placeItem(place)
		if item == "" {
			return result{}, errors.New("unknown place")
		}
		mv = market.MoveGather(place)
		describe = func(v int64) string {
			if v == 0 {
				return fmt.Sprintf("Nothing grows at the %s this season. You came back empty handed.", placeName(place))
			}
			return fmt.Sprintf("You gathered %d %s at the %s.", v, merchant.Plural(item, v), placeName(place))
		}
	case "sell":
		if amount < 1 || amount > 50 {
			return result{}, errors.New("choose 1 to 50")
		}
		q, err := s.Market.Quote(ctx, merchantID, token, "sell", amount)
		if err != nil {
			return result{}, err
		}
		if q.Total == nil || *q.Total == 0 {
			return result{}, fmt.Errorf("%s will not buy any more %s", shopName, merchant.Plural(token, 2))
		}
		mv = market.MoveSell(merchantID, token, amount, int64(*q.Total)*95/100)
		describe = func(v int64) string {
			return fmt.Sprintf("%s paid you %d kauri for %d %s.", shopName, v, amount, merchant.Plural(token, amount))
		}
		feed = func(v int64) string {
			return fmt.Sprintf("sold %d %s to %s for %d kauri", amount, merchant.Plural(token, amount), shopName, v)
		}
	case "buy":
		if amount < 1 || amount > 50 {
			return result{}, errors.New("choose 1 to 50")
		}
		q, err := s.Market.Quote(ctx, merchantID, token, "buy", amount)
		if err != nil {
			return result{}, err
		}
		if q.Total == nil {
			return result{}, fmt.Errorf("%s does not have %d %s", shopName, amount, merchant.Plural(token, amount))
		}
		mv = market.MoveBuy(merchantID, token, amount, int64(*q.Total)*105/100+1)
		describe = func(v int64) string {
			return fmt.Sprintf("You bought %d %s from %s for %d kauri.", amount, merchant.Plural(token, amount), shopName, v)
		}
		feed = func(v int64) string {
			return fmt.Sprintf("bought %d %s from %s for %d kauri", amount, merchant.Plural(token, amount), shopName, v)
		}
	case "craft":
		if amount < 1 || amount > 50 {
			return result{}, errors.New("choose 1 to 50")
		}
		mv = market.MoveCraft("pickle", amount)
		describe = func(v int64) string {
			return fmt.Sprintf("You made %d %s.", v, merchant.Plural("pickle", v))
		}
	case "invest":
		if amount < 10 {
			return result{}, errors.New("invest at least 10 kauri")
		}
		mv = market.MoveInvest(merchantID, amount)
		describe = func(v int64) string {
			return fmt.Sprintf("You invested %d kauri in %s's shop and got %d shares.", amount, shopName, v)
		}
		feed = func(v int64) string {
			return fmt.Sprintf("invested %d kauri in %s's shop", amount, shopName)
		}
	case "claim":
		mv = market.MoveClaim(merchantID)
		describe = func(v int64) string {
			if v == 0 {
				return "No dividends waiting yet. They arrive when the shop closes a profitable day."
			}
			return fmt.Sprintf("You collected %d kauri of dividends from %s.", v, shopName)
		}
	default:
		return result{}, errors.New("unknown action")
	}
	out, err := players.Act(ctx, s.Relayer, &players.Player{AccountID: sess.Account, Play: key}, market.Play(mv))
	if err != nil {
		return result{}, err
	}
	if merchantID != "" && kind != "claim" {
		s.Keeper.Nudge(merchantID)
	}
	var vals []market.U128
	_ = json.Unmarshal(out.Value, &vals)
	var v int64
	if len(vals) > 0 {
		v = int64(vals[0])
	}
	return result{message: describe(v), feed: feed(v), tx: out.Hash}, nil
}

func (s *Server) shopName(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if snap, err := s.snapshot(ctx, false); err == nil {
		for _, m := range snap.Merchants {
			if m.AccountID == id {
				return m.Name
			}
		}
	}
	return "the shop"
}

// friendlyError turns chain errors into game language.
func friendlyError(err error) string {
	msg := err.Error()
	var te *near.TxError
	if errors.As(err, &te) {
		msg = te.Message
	}
	switch {
	case strings.Contains(msg, "no energy left"):
		return "You are tired. Your energy comes back when the next day begins."
	case strings.Contains(msg, "not enough mango"), strings.Contains(msg, "not enough chilli"):
		return "Pickle needs 2 mangoes and 1 chilli per jar, and your basket is short."
	case strings.Contains(msg, "not enough kauri"):
		return "Your purse is too light for that."
	case strings.Contains(msg, "not enough"):
		return "You do not have that many in your basket."
	case strings.Contains(msg, "price moved"):
		return "Someone traded first and the price moved. Have another look and try again."
	case strings.Contains(msg, "rpc"), strings.Contains(msg, "context deadline"):
		return "The village ledger is slow to answer. Your action may still go through, refresh in a moment."
	}
	msg = strings.TrimPrefix(msg, "Smart contract panicked: ")
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return upperFirst(msg) + "."
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (s *Server) ledger(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.readSession(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	pv, err := s.Market.Player(ctx, sess.Account)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	data := s.basePage(snap, takeFlash(w, r))
	data.Page = "ledger"
	data.Title = "Your ledger"
	data.Player = buildPlayer(sess, pv, snap)
	s.render(w, "ledger", data)
}

// reveal shows the recovery key once, then removes it from the cookie for good.
func (s *Server) reveal(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.readSession(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if sess.Recovery == "" {
		setFlash(w, "err", "Your recovery key was already shown once and is no longer kept anywhere by the game.")
		http.Redirect(w, r, "/ledger", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	pv, err := s.Market.Player(ctx, sess.Account)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	rec, err := near.ParseSecretKey(sess.Recovery)
	if err != nil {
		http.Error(w, "bad key", http.StatusInternalServerError)
		return
	}
	data := s.basePage(snap, nil)
	data.Page = "ledger"
	data.Title = "Your recovery key"
	data.Player = buildPlayer(sess, pv, snap)
	cred, _ := json.MarshalIndent(map[string]string{
		"account_id": sess.Account, "public_key": rec.PublicKeyString(), "private_key": rec.SecretKeyString(),
	}, "", "  ")
	data.Reveal = &revealVM{
		Secret: rec.SecretKeyString(), Public: rec.PublicKeyString(), FileName: sess.Account + ".json",
		DataURL: template.URL("data:application/json;base64," + base64.StdEncoding.EncodeToString(cred)),
	}
	sess.Recovery = ""
	if err := s.writeSession(w, r, sess); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	s.render(w, "ledger", data)
}

// forget clears the cookie. The account stays on chain.
func (s *Server) forget(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) marketPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	hist, err := s.Market.History(ctx, max(snap.World.Day-30, 0), 30)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	today, err := s.Market.Today(ctx)
	if err != nil {
		s.unavailable(w, err)
		return
	}
	data := s.basePage(snap, takeFlash(w, r))
	data.Page = "market"
	data.Title = "Market dashboard"
	if sess, ok := s.readSession(r); ok {
		data.Player = &playerVM{Name: players.DisplayName(sess.Account), Account: sess.Account}
	}
	data.Stalls = s.buildStalls(snap, nil)
	data.Dash = buildDashboard(snap, append(hist, today))
	s.render(w, "market", data)
}
