package merchant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"haat/internal/market"
	"haat/internal/near"
)

// Agent is one shopkeeper: its own NEAR account and key, its temperament,
// and a loop that reviews the shop and signs its own decisions.
type Agent struct {
	Spec    Spec
	Account *near.Account
	Market  *market.Client
	Brain   *Brain // optional: the open model that chooses the levers

	mu       sync.Mutex
	thoughts []Thought
	planDay  int       // game day of the model's current choice
	plan     *Decision // the model's choice for planDay (levers and note)
}

// think makes the day's key choice with the model, at most once per game
// day: later reviews the same day reuse it, and a day that looks like one
// seen before reuses that day's choice without calling the model.
func (a *Agent) think(ctx context.Context, sit Situation, rules Decision, t Thought) (Decision, Thought) {
	day := sit.World.Day
	a.mu.Lock()
	plan, planDay := a.plan, a.planDay
	a.mu.Unlock()
	if plan != nil && planDay == day {
		d := rules
		d.Policies, d.Note, d.Reasons = plan.Policies, plan.Note, plan.Reasons
		t.Source, t.Why = "model", "today's choice, made earlier"
		return d, t
	}
	sig := Signature(sit)
	d, ok := a.Brain.Recall(sig, sit, rules)
	if ok {
		t.Source, t.Why = "remembered", "a day like this one came before, so it kept that choice"
	} else {
		md, err := a.Brain.Think(ctx, sit, rules)
		switch {
		case err == nil:
			d, t.Source = md, "model"
			a.Brain.Remember(sig, md)
		case errors.Is(err, ErrNoBudget):
			t.Why = "the model's free allowance was used up, so the rule book decided"
			return rules, t
		default:
			t.Why = "the model did not answer usefully, so the rule book decided"
			return rules, t
		}
	}
	a.mu.Lock()
	cp := d
	a.plan, a.planDay = &cp, day
	a.mu.Unlock()
	return d, t
}

// Thoughts returns the agent's latest decisions, newest first.
func (a *Agent) Thoughts(n int) []Thought {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Thought
	for i := len(a.thoughts) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, a.thoughts[i])
	}
	return out
}

func (a *Agent) remember(t Thought) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thoughts = append(a.thoughts, t)
	if len(a.thoughts) > 20 {
		a.thoughts = a.thoughts[len(a.thoughts)-20:]
	}
}

// Action is a record of what an agent signed, for the village feed.
type Action struct {
	Merchant string
	Summary  string
	TxHash   string
	Err      string
}

// Review looks at the shop and the world, decides, and signs one transaction
// with every change (city trades, crafting, new levers and note). It sends
// nothing when nothing would change.
func (a *Agent) Review(ctx context.Context) (Decision, *Action, error) {
	return a.review(ctx, false)
}

// Evening is the end-of-day review: the same decisions, plus closing the
// books for the day (settle_day) in the same transaction.
func (a *Agent) Evening(ctx context.Context) (Decision, *Action, error) {
	return a.review(ctx, true)
}

func (a *Agent) review(ctx context.Context, settle bool) (Decision, *Action, error) {
	w, err := a.Market.World(ctx)
	if err != nil {
		return Decision{}, nil, err
	}
	shops, err := a.Market.Merchants(ctx)
	if err != nil {
		return Decision{}, nil, err
	}
	var me market.Merchant
	found := false
	for _, m := range shops {
		if m.AccountID == a.Account.ID {
			me, found = m, true
		}
	}
	if !found {
		return Decision{}, nil, fmt.Errorf("%s is not a shop in the market", a.Account.ID)
	}
	sit := Situation{Spec: a.Spec, Shop: me, World: w}
	rules := Decide(sit)
	d := rules
	thought := Thought{At: time.Now(), Source: "rules", Suggested: Summary(rules)}
	if a.Brain.Enabled() {
		d, thought = a.think(ctx, sit, rules, thought)
	}
	ApplyBounds(a.Spec, &d)
	thought.Chosen, thought.Note = Summary(d), d.Note
	// A new note alone is not worth a transaction, except in the evening,
	// when the books are closed anyway.
	changed := !d.Empty(me) || (settle && d.Note != me.Note)
	if !changed && (!settle || me.SettledToday) {
		return d, nil, nil
	}
	var actions []near.Action
	summary := ""
	if changed {
		actions, summary = a.actionsFor(d)
	}
	if settle && !me.SettledToday {
		actions = append(actions, market.SettleDay())
	}
	out, err := a.Account.Send(ctx, a.Market.Contract, actions...)
	act := &Action{Merchant: a.Spec.Name, Summary: summary}
	if out != nil {
		act.TxHash = out.Hash
		thought.TxHash = out.Hash
		var v market.U128
		if settle && len(out.Value) > 0 && v.UnmarshalJSON(out.Value) == nil && v > 0 {
			act.Summary = strings.TrimPrefix(summary+fmt.Sprintf(", paid %d kauri of dividends", v), ", ")
		}
	}
	if err != nil {
		act.Err = err.Error()
	}
	if changed && err == nil {
		a.remember(thought)
	}
	if act.Summary == "" {
		return d, nil, err // a quiet evening: books closed, nothing else to say
	}
	return d, act, err
}

func (a *Agent) actionsFor(d Decision) ([]near.Action, string) {
	var acts []near.Action
	var parts []string
	for _, k := range SortedKeys(d.CityBuy) {
		acts = append(acts, market.CityBuy(k, d.CityBuy[k]))
		parts = append(parts, fmt.Sprintf("bought %d %s from the city", d.CityBuy[k], Plural(k, d.CityBuy[k])))
	}
	if d.Craft > 0 {
		acts = append(acts, market.Craft("pickle", d.Craft))
		parts = append(parts, fmt.Sprintf("made %d %s", d.Craft, Plural("pickle", d.Craft)))
	}
	for _, k := range SortedKeys(d.CitySell) {
		acts = append(acts, market.CitySell(k, d.CitySell[k]))
		parts = append(parts, fmt.Sprintf("sent %d %s to the city", d.CitySell[k], Plural(k, d.CitySell[k])))
	}
	acts = append(acts, market.SetPolicies(d.Policies, d.Note))
	if len(parts) == 0 {
		parts = append(parts, "set new prices")
	}
	return acts, strings.Join(parts, ", ")
}

// Settle closes the shop's books for the day and funds any dividends.
func (a *Agent) Settle(ctx context.Context) (*Action, error) {
	out, err := a.Account.Send(ctx, a.Market.Contract, market.SettleDay())
	act := &Action{Merchant: a.Spec.Name, Summary: "closed the books for the day"}
	if out != nil {
		act.TxHash = out.Hash
		var v market.U128
		if len(out.Value) > 0 && v.UnmarshalJSON(out.Value) == nil && v > 0 {
			act.Summary = fmt.Sprintf("closed the books and paid %d kauri of dividends", v)
		}
	}
	if err != nil {
		act.Err = err.Error()
		if strings.Contains(err.Error(), "already settled") {
			return act, nil
		}
	}
	return act, err
}
