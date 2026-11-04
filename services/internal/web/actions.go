package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// queuedMove is one checked player move waiting to be relayed.
type queuedMove struct {
	kind, merchant, token string
	amount                int64
	mv                    market.Move
	preview               string             // shown at once, before the chain confirms
	describe              func(int64) string // told to the player afterwards
	gathered              string             // item, for gathering moves (results are added up)
	feed                  func(int64) string // told to the village, if anything
}

// reservation is what queued moves will use up, so later taps are checked
// against what will be left rather than what is on chain now.
type reservation struct {
	energy int
	items  map[string]int64
	kauri  int64
}

func (q *queuedMove) reserve(r *reservation) {
	switch q.kind {
	case "gather":
		r.energy++
	case "sell":
		r.items[q.token] += q.amount
	case "craft":
		r.items["mango"] += 2 * q.amount
		r.items["chilli"] += q.amount
	case "buy", "invest":
		r.kauri += q.amount // for buy, amount is set to the price limit below
	}
}

// prepare checks a move against the player's state minus moves already queued,
// so moves that would obviously fail are refused before any gas is spent.
func (s *Server) prepare(ctx context.Context, sess Session, f map[string][]string) (*queuedMove, error) {
	pv, err := s.Market.Player(ctx, sess.Account)
	if err != nil {
		return nil, err
	}
	res := s.queue.reserved(sess.Account)
	kind := first(f, "kind")
	merchantID := first(f, "merchant")
	token := first(f, "token")
	amount, _ := strconv.ParseInt(first(f, "amount"), 10, 64)
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		return nil, err
	}
	var shop *market.Merchant
	for i := range snap.Merchants {
		if snap.Merchants[i].AccountID == merchantID {
			shop = &snap.Merchants[i]
		}
	}
	needShop := func() error {
		if shop == nil {
			return errors.New("There is no such stall.")
		}
		return nil
	}
	q := &queuedMove{kind: kind, merchant: merchantID, token: token, amount: amount, feed: func(int64) string { return "" }}
	switch kind {
	case "gather":
		place := first(f, "place")
		item := placeItem(place)
		if item == "" {
			return nil, errors.New("There is no such place.")
		}
		if pv.EnergyLeft-res.energy <= 0 {
			return nil, errors.New("You are tired. Your energy comes back when the next day begins.")
		}
		if market.BaseYield(snap.World.Season, item) == 0 {
			return nil, fmt.Errorf("Nothing grows at the %s this season.", placeName(place))
		}
		q.mv = market.MoveGather(place)
		q.gathered = item
		q.preview = fmt.Sprintf("Walking to the %s", placeName(place))
		q.describe = func(v int64) string {
			return fmt.Sprintf("You gathered %d %s at the %s.", v, merchant.Plural(item, v), placeName(place))
		}
	case "sell":
		if err := needShop(); err != nil {
			return nil, err
		}
		if amount < 1 || amount > 50 {
			return nil, errors.New("Choose 1 to 50.")
		}
		if !shop.Open {
			return nil, fmt.Errorf("%s's shop is closed until someone invests.", shop.Name)
		}
		if have := pv.Balance(token) - res.items[token]; have < amount {
			return nil, fmt.Errorf("You have only %d %s to sell.", max(have, 0), merchant.Plural(token, 2))
		}
		quote, err := s.Market.Quote(ctx, merchantID, token, "sell", amount)
		if err != nil {
			return nil, err
		}
		if quote.Total == nil || *quote.Total == 0 {
			return nil, fmt.Errorf("%s will not buy any more %s.", shop.Name, merchant.Plural(token, 2))
		}
		if int64(quote.MerchantKauri) < int64(*quote.Total) {
			return nil, fmt.Errorf("%s cannot afford that: only %d kauri in the cash box.", shop.Name, quote.MerchantKauri)
		}
		name := shop.Name
		q.mv = market.MoveSell(merchantID, token, amount, int64(*quote.Total)*90/100)
		q.preview = fmt.Sprintf("Selling %d %s to %s for about %d", amount, merchant.Plural(token, amount), name, *quote.Total)
		q.describe = func(v int64) string {
			return fmt.Sprintf("%s paid you %d kauri for %d %s.", name, v, amount, merchant.Plural(token, amount))
		}
		q.feed = func(v int64) string {
			return fmt.Sprintf("sold %d %s to %s for %d kauri", amount, merchant.Plural(token, amount), name, v)
		}
	case "buy":
		if err := needShop(); err != nil {
			return nil, err
		}
		if amount < 1 || amount > 50 {
			return nil, errors.New("Choose 1 to 50.")
		}
		if !shop.Open {
			return nil, fmt.Errorf("%s's shop is closed until someone invests.", shop.Name)
		}
		quote, err := s.Market.Quote(ctx, merchantID, token, "buy", amount)
		if err != nil {
			return nil, err
		}
		if quote.Total == nil {
			return nil, fmt.Errorf("%s does not have %d %s.", shop.Name, amount, merchant.Plural(token, amount))
		}
		limit := int64(*quote.Total)*110/100 + 1
		if pv.Balance("kauri")-res.kauri < limit {
			return nil, errors.New("Your purse is too light for that.")
		}
		name := shop.Name
		q.mv = market.MoveBuy(merchantID, token, amount, limit)
		q.amount = limit
		q.preview = fmt.Sprintf("Buying %d %s from %s for about %d", amount, merchant.Plural(token, amount), name, *quote.Total)
		q.describe = func(v int64) string {
			return fmt.Sprintf("You bought %d %s from %s for %d kauri.", amount, merchant.Plural(token, amount), name, v)
		}
		q.feed = func(v int64) string {
			return fmt.Sprintf("bought %d %s from %s for %d kauri", amount, merchant.Plural(token, amount), name, v)
		}
	case "craft":
		if amount < 1 || amount > 50 {
			return nil, errors.New("Choose 1 to 50.")
		}
		if pv.Balance("mango")-res.items["mango"] < 2*amount || pv.Balance("chilli")-res.items["chilli"] < amount {
			return nil, errors.New("Pickle needs 2 mangoes and 1 chilli per jar, and your basket is short.")
		}
		q.mv = market.MoveCraft("pickle", amount)
		q.preview = fmt.Sprintf("Making %d %s", amount, merchant.Plural("pickle", amount))
		q.describe = func(v int64) string { return fmt.Sprintf("You made %d %s.", v, merchant.Plural("pickle", v)) }
	case "invest":
		if err := needShop(); err != nil {
			return nil, err
		}
		if amount < 10 {
			return nil, errors.New("Invest at least 10 kauri.")
		}
		if pv.Balance("kauri")-res.kauri < amount {
			return nil, errors.New("Your purse is too light for that.")
		}
		name := shop.Name
		q.mv = market.MoveInvest(merchantID, amount)
		q.preview = fmt.Sprintf("Investing %d kauri in %s's shop", amount, name)
		q.describe = func(v int64) string {
			return fmt.Sprintf("You invested %d kauri in %s's shop and got %d shares.", amount, name, v)
		}
		q.feed = func(v int64) string { return fmt.Sprintf("invested %d kauri in %s's shop", amount, name) }
	case "claim":
		if err := needShop(); err != nil {
			return nil, err
		}
		var pending int64
		for _, h := range pv.Holdings {
			if h.MerchantID == merchantID {
				pending = int64(h.PendingDividends)
			}
		}
		if pending == 0 {
			return nil, errors.New("No dividends waiting yet. They arrive when the shop closes a day above its best so far.")
		}
		name := shop.Name
		q.mv = market.MoveClaim(merchantID)
		q.preview = fmt.Sprintf("Collecting %d kauri from %s", pending, name)
		q.describe = func(v int64) string { return fmt.Sprintf("You collected %d kauri of dividends from %s.", v, name) }
	default:
		return nil, errors.New("Unknown action.")
	}
	return q, nil
}

// moveQueue holds each player's pending moves.
type moveQueue struct {
	mu   sync.Mutex
	byID map[string]*playerQueue
}

type playerQueue struct {
	sess     Session
	moves    []*queuedMove
	inflight []*queuedMove
	done     []doneItem // results waiting to be shown
	errs     []string
	touched  time.Time
}

func newMoveQueue() *moveQueue { return &moveQueue{byID: map[string]*playerQueue{}} }

func (m *moveQueue) push(sess Session, q *queuedMove) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	pq := m.byID[sess.Account]
	if pq == nil {
		pq = &playerQueue{}
		m.byID[sess.Account] = pq
	}
	pq.sess = sess
	pq.moves = append(pq.moves, q)
	pq.touched = time.Now()
	return len(pq.moves) + len(pq.inflight)
}

func (m *moveQueue) reserved(account string) reservation {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := reservation{items: map[string]int64{}}
	if pq := m.byID[account]; pq != nil {
		for _, q := range append(append([]*queuedMove{}, pq.inflight...), pq.moves...) {
			q.reserve(&r)
		}
	}
	return r
}

// status reports pending moves and hands over finished results once.
func (m *moveQueue) status(account string, take bool) (pending int, done []doneItem, errs []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pq := m.byID[account]
	if pq == nil {
		return 0, nil, nil
	}
	pending = len(pq.moves) + len(pq.inflight)
	if take {
		done, errs = pq.done, pq.errs
		pq.done, pq.errs = nil, nil
	}
	return pending, done, errs
}

// next takes up to 10 moves from each idle queue.
func (m *moveQueue) next() map[string][]*queuedMove {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]*queuedMove{}
	for id, pq := range m.byID {
		if len(pq.inflight) > 0 || len(pq.moves) == 0 {
			if len(pq.moves) == 0 && len(pq.inflight) == 0 && len(pq.done) == 0 && len(pq.errs) == 0 && time.Since(pq.touched) > time.Hour {
				delete(m.byID, id)
			}
			continue
		}
		n := min(len(pq.moves), 10)
		pq.inflight, pq.moves = pq.moves[:n], pq.moves[n:]
		out[id] = pq.inflight
	}
	return out
}

func (m *moveQueue) finish(account string, done []doneItem, errs []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pq := m.byID[account]; pq != nil {
		pq.inflight = nil
		pq.done = append(pq.done, done...)
		pq.errs = append(pq.errs, errs...)
		pq.touched = time.Now()
	}
}

func (m *moveQueue) session(account string) Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byID[account].sess
}

// runQueue flushes every player's queue about once a second.
func (s *Server) runQueue(ctx context.Context) {
	t := time.NewTicker(900 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for account, moves := range s.queue.next() {
				go func(account string, moves []*queuedMove) {
					c, cancel := context.WithTimeout(ctx, 60*time.Second)
					defer cancel()
					done, errs := s.flushNow(c, s.queue.session(account), moves)
					s.queue.finish(account, done, errs)
					s.invalidate()
				}(account, moves)
			}
		}
	}
}

// flushNow relays moves as one play() call. Because play() is all or nothing,
// a failed batch is retried one move at a time so one bad move does not sink
// the rest.
func (s *Server) flushNow(ctx context.Context, sess Session, moves []*queuedMove) (done []doneItem, errs []string) {
	if err := s.budget.spend(ctx, s); err != nil {
		return nil, []string{err.Error()}
	}
	key, err := sess.PlayKey()
	if err != nil {
		return nil, []string{"Your pass is damaged. Use Your ledger to start again."}
	}
	p := &players.Player{AccountID: sess.Account, Name: players.DisplayName(sess.Account), Play: key}
	run := func(batch []*queuedMove) (*near.Outcome, error) {
		mv := make([]market.Move, len(batch))
		for i, q := range batch {
			mv[i] = q.mv
		}
		return players.Act(ctx, s.Relayer, p, market.Play(mv...))
	}
	report := func(batch []*queuedMove, out *near.Outcome) {
		var vals []market.U128
		_ = json.Unmarshal(out.Value, &vals)
		for i, q := range batch {
			var v int64
			if i < len(vals) {
				v = int64(vals[i])
			}
			done = append(done, doneItem{text: q.describe(v), gathered: q.gathered, n: v})
			if f := q.feed(v); f != "" {
				s.Keeper.Record(world.FeedItem{Who: p.Name, Text: f, TxHash: out.Hash})
			}
			if q.merchant != "" && q.kind != "claim" {
				s.Keeper.Nudge(q.merchant)
			}
		}
	}
	out, err := run(moves)
	if err == nil {
		report(moves, out)
		return done, nil
	}
	if len(moves) == 1 {
		return nil, []string{friendlyError(err)}
	}
	for _, q := range moves {
		if err := s.budget.spend(ctx, s); err != nil {
			errs = append(errs, err.Error())
			break
		}
		out, err := run([]*queuedMove{q})
		if err != nil {
			errs = append(errs, friendlyError(err))
			continue
		}
		report([]*queuedMove{q}, out)
	}
	return done, errs
}

// pending answers the page's poll: how many moves are still on the way.
func (s *Server) pending(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.readSession(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]int{"pending": 0})
		return
	}
	n, _, _ := s.queue.status(sess.Account, false)
	writeJSON(w, http.StatusOK, map[string]int{"pending": n})
}

// gasBudget caps relayed transactions per UTC day and stops when the root
// account runs low, so the shared demo cannot be drained.
type gasBudget struct {
	mu      sync.Mutex
	day     string
	used    int
	Limit   int
	checked time.Time
	low     bool
}

var errBudget = errors.New("The village has used today's gas allowance for the shared demo. Come back tomorrow, or run your own village with make localnet.")

func (g *gasBudget) spend(ctx context.Context, s *Server) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	today := time.Now().UTC().Format("2006-01-02")
	if g.day != today {
		g.day, g.used = today, 0
	}
	if g.Limit > 0 && g.used >= g.Limit {
		return errBudget
	}
	if time.Since(g.checked) > time.Minute && s.Root != nil {
		g.checked = time.Now()
		if bal, ok, err := s.Root.Client().AccountBalance(ctx, s.Root.ID); err == nil && ok {
			g.low = bal.Cmp(s.MinRootNEAR) < 0
			if g.low {
				log.Printf("root balance below the floor; relaying paused")
			}
		}
	}
	if g.low {
		return errBudget
	}
	g.used++
	return nil
}

// Used reports relayed transactions so far today.
func (g *gasBudget) Used() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.used
}

// doneItem is one finished move.
type doneItem struct {
	text     string
	gathered string
	n        int64
}

// summarise turns finished moves into one short message: trips to gather
// the same item are added up, everything else is listed.
func summarise(items []doneItem) string {
	type trip struct{ n, trips int64 }
	totals := map[string]*trip{}
	var order []string
	var parts []string
	for _, it := range items {
		if it.gathered == "" {
			parts = append(parts, it.text)
			continue
		}
		t := totals[it.gathered]
		if t == nil {
			t = &trip{}
			totals[it.gathered] = t
			order = append(order, it.gathered)
		}
		t.n += it.n
		t.trips++
	}
	var g []string
	for _, item := range order {
		t := totals[item]
		if t.trips == 1 {
			g = append(g, fmt.Sprintf("%d %s", t.n, merchant.Plural(item, t.n)))
		} else {
			g = append(g, fmt.Sprintf("%d %s in %d trips", t.n, merchant.Plural(item, t.n), t.trips))
		}
	}
	if len(g) > 0 {
		parts = append([]string{"You gathered " + strings.Join(g, " and ") + "."}, parts...)
	}
	return strings.Join(parts, " ")
}
