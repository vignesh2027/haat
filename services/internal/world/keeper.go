// Package world keeps the village clock: it ends each day (shopkeepers settle
// their books, the calendar moves on), wakes the shopkeepers to review their
// shops, and keeps a feed of what everyone did.
package world

import (
	"context"
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
	Root   *near.Account
	Market *market.Client
	Agents []*merchant.Agent

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
		if err := k.EndDay(ctx); err != nil {
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
