// Package web serves the game as server-rendered HTML. Forms post actions,
// the server signs them with the player's play key and the relayer pays the
// gas, then the page reloads. The browser runs only a few lines of inline JS.
package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
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
	// MinRootNEAR stops new accounts and relaying when the root balance falls below it.
	MinRootNEAR *big.Int
	// TrustProxy believes X-Forwarded-For and X-Forwarded-Proto (set it only behind a known proxy).
	TrustProxy bool
	// PoolSize is how many player accounts are made ahead of time.
	PoolSize int

	sealer *Sealer
	limits *limits
	queue  *moveQueue
	budget *gasBudget
	pool   *accountPool
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
	s := &Server{
		sealer: sealer, cacheTTL: 2 * time.Second, MaxNewPlayersPerHour: 60, MinRootNEAR: near.MustNEAR("0.5"),
		limits: newLimits(), queue: newMoveQueue(), budget: &gasBudget{Limit: 3000},
	}
	if err := s.parse(); err != nil {
		return nil, err
	}
	return s, nil
}

// cssVersion is a short hash of the stylesheet, so browsers reload it after a change.
var cssVersion = func() string {
	b, _ := assets.ReadFile("static/haat.css")
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:4])
}()

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

// SetDailyBudget caps relayed transactions per UTC day (0 means no cap).
func (s *Server) SetDailyBudget(n int) { s.budget.Limit = n }

// Start runs the background work: the move queue and the account pool.
func (s *Server) Start(ctx context.Context) {
	go s.runQueue(ctx)
	if s.PoolSize > 0 {
		s.pool = &accountPool{ch: make(chan *players.Player, s.PoolSize)}
		go s.fillPool(ctx)
	}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(http.FileServerFS(static), 3600)))
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /start", s.start)
	mux.HandleFunc("POST /act", s.act)
	mux.HandleFunc("GET /pending", s.pending)
	mux.HandleFunc("GET /ledger", s.ledger)
	mux.HandleFunc("POST /ledger/reveal", s.reveal)
	mux.HandleFunc("POST /ledger/forget", s.forget)
	mux.HandleFunc("GET /market", s.marketPage)
	mux.HandleFunc("POST /fast-forward", s.fastForward)
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
		snap.Chronicle, cErr = s.Market.Chronicle(ctx, max(n-60, 0), 60)
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
	pending, done, errs := s.queue.status(sess.Account, true)
	data.Pending = pending
	if len(errs) > 0 {
		data.Flash = &flash{Kind: "err", Text: strings.TrimSpace(strings.Join(errs, " ") + " " + summarise(done))}
	} else if len(done) > 0 {
		data.Flash = &flash{Kind: "ok", Text: summarise(done)}
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
	_ = r.ParseForm()
	// The inline script fills this field when the button is pressed; plain
	// scripts posting to /start do not get an account.
	if r.PostForm.Get("walked_in") != "yes" {
		setFlash(w, "err", "Please press the button on the page to enter.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.limits.startPerIP.allow(s.clientIP(r)) {
		setFlash(w, "err", "You have opened several ledgers already this hour. Use the one you have, or come back later.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.allowNewPlayer(ctx) {
		setFlash(w, "err", "The haat is very busy this hour. Please come back a little later.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	var p *players.Player
	var err error
	select {
	case p = <-s.poolCh():
	default:
		p, err = players.Create(ctx, s.Root, s.Market.Contract, players.RandomName())
	}
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

func (s *Server) poolCh() chan *players.Player {
	if s.pool == nil {
		return nil // a nil channel never delivers, so start creates one directly
	}
	return s.pool.ch
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

// act takes one player action. With JavaScript, the page sends it in the
// background and it joins the player's queue, which is flushed as one play()
// call every second or so; the page shows it as pending at once. Without
// JavaScript, the action is relayed straight away and the page reloads.
func (s *Server) act(w http.ResponseWriter, r *http.Request) {
	queued := r.Header.Get("X-Haat-Queue") == "1"
	sess, ok := s.readSession(r)
	if !ok {
		if queued {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Your pass is missing. Reload the page."})
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !s.limits.allowAct(sess.Account, s.clientIP(r)) {
		s.reply(w, r, queued, "", errors.New("Slow down a little: the village ledger writes one page at a time."))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	s.Keeper.Touch()
	q, err := s.prepare(ctx, sess, r.PostForm)
	if err != nil {
		s.reply(w, r, queued, "", err)
		return
	}
	if queued {
		n := s.queue.push(sess, q)
		writeJSON(w, http.StatusAccepted, map[string]any{"pending": n, "preview": q.preview})
		return
	}
	done, errs := s.flushNow(ctx, sess, []*queuedMove{q})
	if len(errs) > 0 {
		setFlash(w, "err", errs[0])
	} else if len(done) > 0 {
		setFlash(w, "ok", summarise(done))
	}
	s.invalidate()
	http.Redirect(w, r, "/#"+anchorFor(r.PostForm), http.StatusSeeOther)
}

// reply reports an error to a background request as JSON, or to a plain form as a flash.
func (s *Server) reply(w http.ResponseWriter, r *http.Request, queued bool, msg string, err error) {
	if queued {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": friendlyError(err)})
		return
	}
	setFlash(w, "err", friendlyError(err))
	http.Redirect(w, r, "/#"+anchorFor(r.PostForm), http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
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
	if strings.HasSuffix(msg, ".") {
		return upperFirst(msg)
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
		setFlash(w, "err", "Your recovery key was already shown once and this browser no longer carries it.")
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

// fastForward starts a week of back-to-back trading.
func (s *Server) fastForward(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.readSession(r)
	if !ok {
		setFlash(w, "err", "Enter the haat first, then you can fast-forward the week.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.limits.ffPerAcct.allow(sess.Account) {
		setFlash(w, "err", "You fast-forwarded a little while ago. Give the village a few minutes.")
		http.Redirect(w, r, "/market", http.StatusSeeOther)
		return
	}
	switch err := s.Keeper.StartFastForward(7); {
	case errors.Is(err, world.ErrCapped):
		setFlash(w, "err", "The village has already fast-forwarded this hour, to keep the shared demo affordable. Run it locally with `make localnet` for as many weeks as you like.")
	case errors.Is(err, world.ErrBusy):
		setFlash(w, "ok", "A fast-forward is already running. Watch the charts move.")
	case err != nil:
		setFlash(w, "err", friendlyError(err))
	default:
		setFlash(w, "ok", "Fast-forward started: a week of trading with the villagers. The page refreshes as each day ends.")
	}
	http.Redirect(w, r, "/market", http.StatusSeeOther)
}
