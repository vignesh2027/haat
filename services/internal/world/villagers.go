package world

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"haat/internal/market"
	"haat/internal/merchant"
	"haat/internal/near"
	"haat/internal/players"
)

// Villagers are NPC players. They use exactly the same path as a human
// player: a play key that can only call the market, moves batched into one
// play() call per day, and gas paid by the relayer. They make the market move
// when no human is around and during fast-forward.

// VillagerRoster names the villagers and their habits.
var VillagerRoster = []struct{ Handle, Habit string }{
	{"gauri", "mango picker"},
	{"imran", "fisher"},
	{"tara", "pickle maker"},
	{"babu", "chilli farmer"},
	{"noor", "trader"},
	{"kishan", "investor"},
}

// Villager is one NPC player.
type Villager struct {
	Player *players.Player
	Habit  string
}

// Plan is one villager's moves for a day, with a plain description.
type Plan struct {
	Moves []market.Move
	Says  []string
}

// View is what a villager knows when it plans its day.
type View struct {
	World        market.World
	Shops        []market.Merchant
	Me           market.Player
	ClosedBefore map[string]bool // shops that were already closed yesterday
	Day          int
}

// bestBid finds the open shop paying the most for one unit of an item.
func bestBid(shops []market.Merchant, item string) (market.Merchant, int64, bool) {
	var best market.Merchant
	var bid int64
	found := false
	for _, m := range shops {
		if !m.Open {
			continue
		}
		if sh, ok := m.Shelf(item); ok && int64(sh.Bid) > bid {
			best, bid, found = m, int64(sh.Bid), true
		}
	}
	return best, bid, found
}

// sellAll plans selling n units, splitting between the two best shops so one
// stall is not flooded, and never more than a shop can pay for.
func sellAll(v View, item string, n int64, p *Plan) {
	if n <= 0 {
		return
	}
	type cand struct {
		m   market.Merchant
		bid int64
	}
	var cs []cand
	for _, m := range v.Shops {
		if sh, ok := m.Shelf(item); ok && m.Open && sh.Bid > 0 {
			cs = append(cs, cand{m, int64(sh.Bid)})
		}
	}
	if len(cs) == 0 {
		return
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].bid > cs[j].bid })
	parts := []int64{n}
	if len(cs) > 1 && n >= 4 {
		parts = []int64{n - n/3, n / 3}
	}
	for i, amt := range parts {
		c := cs[i]
		// Keep well inside what the shop can pay, since prices only fall as it buys.
		afford := c.m.Spendable() / max(c.bid, 1)
		amt = min(amt, afford*2/3, 50)
		if amt <= 0 {
			continue
		}
		p.Moves = append(p.Moves, market.MoveSell(c.m.AccountID, item, amt, 1))
		p.Says = append(p.Says, fmt.Sprintf("sold %d %s to %s", amt, merchant.Plural(item, amt), c.m.Name))
	}
}

func gather(v View, item string, trips int, p *Plan) int64 {
	place := market.PlaceFor(item)
	y := market.BaseYield(v.World.Season, item)
	if y == 0 || trips <= 0 {
		return 0
	}
	for i := 0; i < trips; i++ {
		p.Moves = append(p.Moves, market.MoveGather(place))
	}
	p.Says = append(p.Says, fmt.Sprintf("went to the %s %d times", place, trips))
	return y * int64(trips)
}

// bestItem picks the gatherable item worth the most per trip right now.
func bestItem(v View, items ...string) string {
	best, score := "", int64(-1)
	for _, it := range items {
		_, bid, ok := bestBid(v.Shops, it)
		if !ok {
			continue
		}
		if s := market.BaseYield(v.World.Season, it) * bid; s > score {
			best, score = it, s
		}
	}
	return best
}

// PlanDay decides a villager's moves. It is deterministic for a given view.
func PlanDay(habit string, v View) Plan {
	var p Plan
	energy := v.Me.EnergyLeft
	trips := min(energy, 4)
	switch habit {
	case "mango picker", "fisher", "chilli farmer":
		pref := map[string]string{"mango picker": "mango", "fisher": "fish", "chilli farmer": "chilli"}[habit]
		item := pref
		if market.BaseYield(v.World.Season, item) == 0 {
			item = bestItem(v, "mango", "rice", "fish", "chilli")
		}
		got := gather(v, item, trips, &p)
		sellAll(v, item, v.Me.Balance(item)+got, &p)
	case "pickle maker":
		mango := v.Me.Balance("mango") + gather(v, "mango", min(trips, 2), &p)
		chilli := v.Me.Balance("chilli") + gather(v, "chilli", min(trips-min(trips, 2), 2), &p)
		jars := min(mango/2, chilli)
		if jars > 0 {
			p.Moves = append(p.Moves, market.MoveCraft("pickle", jars))
			p.Says = append(p.Says, fmt.Sprintf("made %d %s", jars, merchant.Plural("pickle", jars)))
		}
		sellAll(v, "pickle", v.Me.Balance("pickle")+jars, &p)
	case "trader":
		// Buy where it is cheap, sell where it is dear, if the gap is worth it.
		bestGap, buyFrom, sellTo, item := int64(0), market.Merchant{}, market.Merchant{}, ""
		for _, a := range v.Shops {
			for _, sh := range a.Shelves {
				if !a.Open || sh.Ask == nil || sh.Stock < 3 {
					continue
				}
				b, bid, ok := bestBid(v.Shops, sh.TokenID)
				if ok && b.AccountID != a.AccountID && bid-int64(*sh.Ask) > bestGap {
					bestGap, buyFrom, sellTo, item = bid-int64(*sh.Ask), a, b, sh.TokenID
				}
			}
		}
		if item != "" && bestGap*10 > int64(*mustAsk(buyFrom, item)) {
			n := min(3, v.Me.Balance("kauri")/(int64(*mustAsk(buyFrom, item))*2))
			if n > 0 {
				p.Moves = append(p.Moves, market.MoveBuy(buyFrom.AccountID, item, n, int64(*mustAsk(buyFrom, item))*n*2))
				p.Moves = append(p.Moves, market.MoveSell(sellTo.AccountID, item, n, 1))
				p.Says = append(p.Says, fmt.Sprintf("bought %d %s from %s and sold them to %s", n, merchant.Plural(item, n), buyFrom.Name, sellTo.Name))
			}
		}
		item = bestItem(v, "mango", "rice", "fish", "chilli")
		got := gather(v, item, min(trips, 3), &p)
		sellAll(v, item, v.Me.Balance(item)+got, &p)
	case "investor":
		for _, m := range v.Shops {
			if !m.Open && v.ClosedBefore[m.AccountID] && v.Me.Balance("kauri") >= 300 {
				p.Moves = append(p.Moves, market.MoveInvest(m.AccountID, 300))
				p.Says = append(p.Says, fmt.Sprintf("invested 300 kauri to reopen %s's shop", m.Name))
				break
			}
		}
		for _, h := range v.Me.Holdings {
			if h.PendingDividends > 0 {
				p.Moves = append(p.Moves, market.MoveClaim(h.MerchantID))
				p.Says = append(p.Says, fmt.Sprintf("collected %d kauri of dividends", h.PendingDividends))
			}
		}
		item := bestItem(v, "rice", "fish")
		got := gather(v, item, min(trips, 3), &p)
		sellAll(v, item, v.Me.Balance(item)+got, &p)
	}
	if len(p.Moves) > 10 {
		p.Moves = p.Moves[:10]
	}
	return p
}

func mustAsk(m market.Merchant, item string) *market.U128 {
	sh, _ := m.Shelf(item)
	if sh.Ask == nil {
		z := market.U128(1)
		return &z
	}
	return sh.Ask
}

// Act plans and plays a villager's day. If the full plan fails (another
// villager moved a price first), it falls back to gathering only.
func (vl *Villager) Act(ctx context.Context, r *near.Relayer, mc *market.Client, base View) (string, string, error) {
	me, err := mc.Player(ctx, vl.Player.AccountID)
	if err != nil {
		return "", "", err
	}
	v := base
	v.Me = me
	plan := PlanDay(vl.Habit, v)
	if len(plan.Moves) == 0 {
		return "rested today", "", nil
	}
	out, err := players.Act(ctx, r, vl.Player, market.Play(plan.Moves...))
	if err == nil {
		return strings.Join(plan.Says, ", "), out.Hash, nil
	}
	var safe Plan
	for i, m := range plan.Moves {
		if _, ok := m["gather"]; ok {
			safe.Moves = append(safe.Moves, plan.Moves[i])
		}
	}
	if len(safe.Moves) == 0 {
		return "", "", err
	}
	out, err2 := players.Act(ctx, r, vl.Player, market.Play(safe.Moves...))
	if err2 != nil {
		return "", "", err2
	}
	return "gathered and kept everything for tomorrow", out.Hash, nil
}
