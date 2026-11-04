package merchant

import (
	"context"
	"fmt"
	"strings"

	"haat/internal/market"
	"haat/internal/near"
)

// Agent is one shopkeeper: its own NEAR account and key, its temperament,
// and a loop that reviews the shop and signs its own decisions.
type Agent struct {
	Spec    Spec
	Account *near.Account
	Market  *market.Client
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
	d := Decide(Situation{Spec: a.Spec, Shop: me, World: w})
	changed := !d.Empty(me) || d.Note != me.Note
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
		var v market.U128
		if settle && len(out.Value) > 0 && v.UnmarshalJSON(out.Value) == nil && v > 0 {
			act.Summary = strings.TrimPrefix(summary+fmt.Sprintf(", paid %d kauri of dividends", v), ", ")
		}
	}
	if err != nil {
		act.Err = err.Error()
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
