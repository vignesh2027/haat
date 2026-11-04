// Package world keeps the village clock: it ends each day (shopkeepers settle
// their books, the calendar moves on), wakes the shopkeepers to review their
// shops, and keeps a feed of what everyone did.
package world

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"haat/internal/market"
	"haat/internal/merchant"
	"haat/internal/near"
)

// FeedItem is one line in the village feed.
type FeedItem struct {
	At     time.Time
	Day    int
	Who    string
	Text   string
	TxHash string
	Failed bool
}

// Keeper runs the clock and the shopkeeper agents.
type Keeper struct {
	Root      *near.Account
	Market    *market.Client
	Agents    []*merchant.Agent
	Relayer   *near.Relayer
	Villagers []*Villager
	// FastForwardsPerHour caps fast-forward runs, to keep testnet spend low.
	FastForwardsPerHour int

	DayLength   time.Duration // real time per game day
	ReviewEvery time.Duration // routine shop reviews while players are around
	IdleAfter   time.Duration // the village sleeps when nobody has played for this long

	mu         sync.Mutex
	feed       []FeedItem
	lastActive time.Time
	nextDay    time.Time
	lastReview time.Time
	nudged     map[string]time.Time
	dayLock    sync.Mutex // held while a day changes or a fast-forward runs
	day        int
	closed     map[string]bool // shops closed at the end of the previous day
	ff         FastForward
	ffRuns     []time.Time
}

// FastForward is the state of a fast-forward run, for the progress display.
type FastForward struct {
	Running   bool
	Day, Of   int
	StartDay  int
	StartedAt time.Time
	Took      time.Duration
	Err       string
}

// Touch records player activity, which keeps the clock running.
func (k *Keeper) Touch() {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	if k.nextDay.IsZero() || now.Sub(k.lastActive) > k.IdleAfter {
		k.nextDay = now.Add(k.DayLength)
	}
	k.lastActive = now
}

// Nudge asks a shopkeeper to look at its shop soon, after a player traded with it.
func (k *Keeper) Nudge(merchantID string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.nudged == nil {
		k.nudged = map[string]time.Time{}
	}
	if _, ok := k.nudged[merchantID]; !ok {
		k.nudged[merchantID] = time.Now().Add(15 * time.Second)
	}
}

// Awake reports whether players have been around recently.
func (k *Keeper) Awake() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return time.Since(k.lastActive) < k.IdleAfter
}

// NextDayIn is the real time left in the current game day.
func (k *Keeper) NextDayIn() time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.nextDay.IsZero() {
		return k.DayLength
	}
	return max(time.Until(k.nextDay), 0)
}

// Feed returns the latest feed items, newest first.
func (k *Keeper) Feed(n int) []FeedItem {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]FeedItem, 0, n)
	for i := len(k.feed) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, k.feed[i])
	}
	return out
}

// Record adds a line to the feed.
func (k *Keeper) Record(item FeedItem) {
	if item.At.IsZero() {
		item.At = time.Now()
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if item.Day == 0 {
		item.Day = k.day
	}
	k.feed = append(k.feed, item)
	if len(k.feed) > 200 {
		k.feed = k.feed[len(k.feed)-200:]
	}
}

func (k *Keeper) recordAction(a *merchant.Action) {
	if a == nil {
		return
	}
	k.Record(FeedItem{Who: a.Merchant, Text: a.Summary, TxHash: a.TxHash, Failed: a.Err != ""})
}

// Run drives the clock until ctx ends.
func (k *Keeper) Run(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	k.refreshDay(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			k.tick(ctx)
		}
	}
}

func (k *Keeper) refreshDay(ctx context.Context) {
	if w, err := k.Market.World(ctx); err == nil {
		k.mu.Lock()
		k.day = w.Day
		k.mu.Unlock()
	}
}

func (k *Keeper) tick(ctx context.Context) {
	if !k.Awake() {
		return
	}
	k.mu.Lock()
	dayDue := !k.nextDay.IsZero() && time.Now().After(k.nextDay)
	reviewDue := time.Since(k.lastReview) > k.ReviewEvery
	var nudged []string
	for id, at := range k.nudged {
		if time.Now().After(at) {
			nudged = append(nudged, id)
			delete(k.nudged, id)
		}
	}
	k.mu.Unlock()

	switch {
	case dayDue:
		if !k.dayLock.TryLock() {
			return // a fast-forward is running
		}
		err := k.endDayLocked(ctx)
		k.dayLock.Unlock()
		if err != nil {
			log.Printf("end of day: %v", err)
		}
		k.mu.Lock()
		k.nextDay = time.Now().Add(k.DayLength)
		k.mu.Unlock()
	case reviewDue:
		k.ReviewAll(ctx)
	case len(nudged) > 0:
		k.review(ctx, nudged)
	}
}

// ReviewAll asks every shopkeeper to review its shop, in parallel.
func (k *Keeper) ReviewAll(ctx context.Context) {
	ids := make([]string, len(k.Agents))
	for i, a := range k.Agents {
		ids[i] = a.Account.ID
	}
	k.review(ctx, ids)
	k.mu.Lock()
	k.lastReview = time.Now()
	k.mu.Unlock()
}

func (k *Keeper) review(ctx context.Context, ids []string) {
	if !k.dayLock.TryLock() {
		return
	}
	defer k.dayLock.Unlock()
	k.reviewLocked(ctx, ids)
}

func (k *Keeper) reviewLocked(ctx context.Context, ids []string) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var wg sync.WaitGroup
	for _, a := range k.Agents {
		if !want[a.Account.ID] {
			continue
		}
		wg.Add(1)
		go func(a *merchant.Agent) {
			defer wg.Done()
			_, act, err := a.Review(ctx)
			if err != nil {
				log.Printf("%s review: %v", a.Spec.Key, err)
			}
			k.recordAction(act)
		}(a)
	}
	wg.Wait()
}

// EndDay settles every shop, moves the calendar on and wakes the shopkeepers
// for the new morning.
func (k *Keeper) EndDay(ctx context.Context) error {
	k.dayLock.Lock()
	defer k.dayLock.Unlock()
	return k.endDayLocked(ctx)
}

func (k *Keeper) endDayLocked(ctx context.Context) error {
	k.villagersRound(ctx)
	var wg sync.WaitGroup
	for _, a := range k.Agents {
		wg.Add(1)
		go func(a *merchant.Agent) {
			defer wg.Done()
			act, err := a.Settle(ctx)
			if err != nil {
				log.Printf("%s settle: %v", a.Spec.Key, err)
			}
			if act != nil && (act.Err != "" || act.Summary != "closed the books for the day") {
				k.recordAction(act)
			}
		}(a)
	}
	wg.Wait()
	out, err := k.Root.Send(ctx, k.Market.Contract, market.AdvanceDay())
	if err != nil {
		return err
	}
	k.refreshDay(ctx)
	if shops, err := k.Market.Merchants(ctx); err == nil {
		closed := map[string]bool{}
		for _, m := range shops {
			closed[m.AccountID] = !m.Open
		}
		k.mu.Lock()
		k.closed = closed
		k.mu.Unlock()
	}
	w, err := k.Market.World(ctx)
	if err == nil {
		text := "A new day begins. " + SeasonLabel(w.Season) + "."
		if w.Flood {
			text += " Heavy rain: the road to the city is flooded."
		}
		k.Record(FeedItem{Day: w.Day, Who: "The village", Text: text, TxHash: out.Hash})
	}
	ids := make([]string, len(k.Agents))
	for i, a := range k.Agents {
		ids[i] = a.Account.ID
	}
	k.reviewLocked(ctx, ids)
	return nil
}

// SeasonLabel names a season for players.
func SeasonLabel(s string) string {
	switch s {
	case "garmi":
		return "Garmi, the hot summer"
	case "barsaat":
		return "Barsaat, the monsoon"
	case "tyohar":
		return "Tyohar, the festival season"
	}
	return s
}

// villagersRound lets every villager play its day, in parallel.
func (k *Keeper) villagersRound(ctx context.Context) {
	if len(k.Villagers) == 0 || k.Relayer == nil {
		return
	}
	w, err := k.Market.World(ctx)
	if err != nil {
		return
	}
	shops, err := k.Market.Merchants(ctx)
	if err != nil {
		return
	}
	k.mu.Lock()
	closedBefore := map[string]bool{}
	for id, c := range k.closed {
		closedBefore[id] = c
	}
	k.mu.Unlock()
	base := View{World: w, Shops: shops, ClosedBefore: closedBefore, Day: w.Day}
	var wg sync.WaitGroup
	for _, v := range k.Villagers {
		wg.Add(1)
		go func(v *Villager) {
			defer wg.Done()
			text, hash, err := v.Act(ctx, k.Relayer, k.Market, base)
			if err != nil {
				log.Printf("villager %s: %v", v.Player.Name, err)
				return
			}
			k.Record(FeedItem{Who: v.Player.Name, Text: text, TxHash: hash})
		}(v)
	}
	wg.Wait()
}

// StartFastForward runs `days` game days back to back in the background.
// It refuses when a run is already going or the hourly cap is reached.
func (k *Keeper) StartFastForward(days int) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.ff.Running {
		return ErrBusy
	}
	cut := time.Now().Add(-time.Hour)
	kept := k.ffRuns[:0]
	for _, t := range k.ffRuns {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	k.ffRuns = kept
	if k.FastForwardsPerHour > 0 && len(k.ffRuns) >= k.FastForwardsPerHour {
		return ErrCapped
	}
	k.ffRuns = append(k.ffRuns, time.Now())
	k.ff = FastForward{Running: true, Of: days, StartDay: k.day, StartedAt: time.Now()}
	k.lastActive = time.Now()
	go k.runFastForward(days)
	return nil
}

func (k *Keeper) runFastForward(days int) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	k.dayLock.Lock()
	defer k.dayLock.Unlock()
	k.Record(FeedItem{Who: "The village", Text: fmt.Sprintf("Fast-forward: %d days of trading begin.", days)})
	var err error
	for d := 1; d <= days && err == nil; d++ {
		k.mu.Lock()
		k.ff.Day = d
		k.mu.Unlock()
		err = k.endDayLocked(ctx)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ff.Running = false
	k.ff.Took = time.Since(k.ff.StartedAt)
	if err != nil {
		k.ff.Err = err.Error()
		log.Printf("fast-forward: %v", err)
	}
	k.nextDay = time.Now().Add(k.DayLength)
	k.lastActive = time.Now()
}

// FastForwardState returns the current or last fast-forward run.
func (k *Keeper) FastForwardState() FastForward {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.ff
}

// FastForwardsLeft is how many runs the hourly cap still allows.
func (k *Keeper) FastForwardsLeft() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.FastForwardsPerHour <= 0 {
		return 99
	}
	n := 0
	for _, t := range k.ffRuns {
		if time.Since(t) < time.Hour {
			n++
		}
	}
	return max(k.FastForwardsPerHour-n, 0)
}

var (
	// ErrBusy means a fast-forward is already running.
	ErrBusy = errors.New("a fast-forward is already running")
	// ErrCapped means the hourly fast-forward cap is reached.
	ErrCapped = errors.New("the village has fast-forwarded enough this hour")
)
