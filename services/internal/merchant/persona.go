package merchant

import (
	"fmt"
	"sort"
	"strings"

	"haat/internal/market"
)

// The rules every shopkeeper follows. Each temperament is a small set of
// deterministic rules, so the economy is testable: the same chain state always
// gives the same decision. Prices themselves are set by the contract from the
// levers chosen here (see contract/src/pricing.rs).

const (
	closeBelow    = 200 // matches CLOSE_BELOW in the contract
	cashCrunch    = 450 // below this a sensible shop raises cash
	maxCityTrade  = 50  // matches MAX_TRADE in the contract
	pickleKeep    = 4   // pickle Lakshmi keeps for the stall in the festival
	ramuReservePc = 60  // Ramu keeps 60% of his opening purse in cash
)

// Decision is what a shopkeeper does at one review.
type Decision struct {
	Policies []market.Policy
	CitySell map[string]int64
	CityBuy  map[string]int64
	Craft    int64
	Reasons  []string // plain words, one per rule that fired
	Note     string   // short first-person note stored on chain (at most 200 characters)
}

// Empty reports whether the decision changes nothing on chain.
func (d Decision) Empty(current market.Merchant) bool {
	return len(d.CitySell) == 0 && len(d.CityBuy) == 0 && d.Craft == 0 && !policiesChanged(d.Policies, current)
}

func policiesChanged(p []market.Policy, m market.Merchant) bool {
	for _, x := range p {
		s, ok := m.Shelf(x.TokenID)
		if !ok || s.TargetStock != x.TargetStock || s.MarginBps != x.MarginBps {
			return true
		}
	}
	return false
}

// Situation is everything a shopkeeper looks at.
type Situation struct {
	Spec  Spec
	Shop  market.Merchant
	World market.World
}

func (s Situation) floodSoon(days int) bool {
	if s.World.Flood {
		return true
	}
	for i, f := range s.World.Forecast {
		if i < days && f.Flood {
			return true
		}
	}
	return false
}

func (s Situation) roadOpen() bool { return !s.World.Flood }

// seasonSoon reports whether the panchang shows a season starting within days.
func (s Situation) seasonSoon(season string, days int) bool {
	for i, f := range s.World.Forecast {
		if i < days && f.Season == season {
			return true
		}
	}
	return false
}

// Decide applies the shopkeeper's temperament to the situation.
func Decide(s Situation) Decision {
	d := Decision{CitySell: map[string]int64{}, CityBuy: map[string]int64{}}
	switch s.Spec.Key {
	case "ramu":
		decideRamu(s, &d)
	case "meena":
		decideMeena(s, &d)
	case "bashir":
		decideBashir(s, &d)
	case "lakshmi":
		decideLakshmi(s, &d)
	default:
		for _, it := range s.Spec.Items {
			d.Policies = append(d.Policies, market.Policy{TokenID: it, TargetStock: 20, MarginBps: 1000})
		}
	}
	clampCity(s, &d)
	ApplyBounds(s.Spec, &d)
	d.Note = composeNote(s, d)
	return d
}

// ApplyBounds keeps a decision inside the shopkeeper's character: margins and
// targets inside the bounds the contract also enforces. The agent applies it
// to every decision, from the rule book or the model, before signing.
func ApplyBounds(spec Spec, d *Decision) {
	b := spec.Bounds
	if b.MarginMax == 0 {
		return
	}
	for i := range d.Policies {
		d.Policies[i].MarginBps = clampInt(d.Policies[i].MarginBps, b.MarginMin, b.MarginMax)
		d.Policies[i].TargetStock = clampInt(d.Policies[i].TargetStock, 1, b.TargetMax)
	}
}

func decideRamu(s Situation, d *Decision) {
	reserve := s.Spec.OpeningKauri * ramuReservePc / 100
	cash := int64(s.Shop.Kauri)
	short := cash < reserve
	for _, it := range s.Spec.Items {
		target := 15
		if short {
			target = 5
		}
		d.Policies = append(d.Policies, market.Policy{TokenID: it, TargetStock: target, MarginBps: 2000})
		stock := s.Shop.StockOf(it)
		if s.roadOpen() && stock > int64(2*target) {
			d.CitySell[it] = stock - int64(target)
		}
	}
	if short {
		d.Reasons = append(d.Reasons, fmt.Sprintf("I keep %d kauri aside and have only %d, so I am buying little.", reserve, cash))
	} else {
		d.Reasons = append(d.Reasons, "I keep a 20% margin and never let my cash box run low.")
	}
	if len(d.CitySell) > 0 {
		d.Reasons = append(d.Reasons, "My shelves are too full, the extra goes to the city cart.")
	}
}

func decideMeena(s Situation, d *Decision) {
	cash := int64(s.Shop.Kauri)
	// With investors watching she is careful, until a full cash box makes her bold again.
	careful := s.Shop.SharesIssued > 0 && cash < s.Spec.OpeningKauri*3/2
	for _, it := range s.Spec.Items {
		target, margin := 30, 600
		if it == "pickle" {
			target = 6
		}
		if careful {
			target, margin = target/3, 1500
		}
		d.Policies = append(d.Policies, market.Policy{TokenID: it, TargetStock: target, MarginBps: margin})
		stock := s.Shop.StockOf(it)
		limit := int64(2 * target)
		if careful {
			limit = int64(target)
		}
		if s.roadOpen() && stock > limit {
			d.CitySell[it] = stock - int64(target)
		}
		// Bold Meena keeps her stall full of chillies whatever the city charges.
		if it == "chilli" && !careful && s.roadOpen() && stock < 8 && cash > 400 {
			d.CityBuy[it] = 12
		}
	}
	// Her big bets: chillies before the rains, pickle before Diwali. She spends
	// her last kauri on them, which is how a bold shop goes broke.
	if !careful && s.roadOpen() && s.Shop.Open {
		switch {
		case s.World.Season == "barsaat" || s.seasonSoon("barsaat", 2):
			if s.Shop.StockOf("chilli") < 60 {
				d.CityBuy["chilli"] = 50
				d.Reasons = append(d.Reasons, "Rain is coming and chillies will be scarce. I am buying every chilli I can afford.")
			}
		case s.World.Season == "tyohar" || s.seasonSoon("tyohar", 2):
			if s.Shop.StockOf("pickle") < 12 {
				d.CityBuy["pickle"] = 12
				d.Reasons = append(d.Reasons, "Diwali crowds will want pickle. I am filling my shelf whatever it costs.")
			}
		}
	}
	switch {
	case !s.Shop.Open:
		d.Reasons = append(d.Reasons, "My cash box is empty. I need an investor to open again.")
	case careful:
		d.Reasons = append(d.Reasons, "My investors trusted me, so now I buy less, keep a wider margin and send extra stock to the city.")
	default:
		d.Reasons = append(d.Reasons, "Thin margin, full shelves. Customers come back to a busy stall.")
	}
	if d.CityBuy["chilli"] > 0 {
		d.Reasons = append(d.Reasons, "Running low on chillies, I am ordering more from the city.")
	}
}

func decideBashir(s Situation, d *Decision) {
	rainSoon := s.floodSoon(2)
	season := s.World.Season
	for _, it := range s.Spec.Items {
		target, margin := 20, 1200
		if it == "fish" && season == "barsaat" {
			target = 12 // the river is full; he expects a glut
		}
		if rainSoon {
			margin = 2000
			if it == "fish" {
				target /= 2
			}
			// Rice is scarce once the road is cut: he stocks up while it is open.
			if it == "rice" && s.roadOpen() {
				if want := int64(target) - s.Shop.StockOf("rice"); want > 0 {
					d.CityBuy["rice"] = want
				}
			}
		}
		d.Policies = append(d.Policies, market.Policy{TokenID: it, TargetStock: target, MarginBps: margin})
		stock := s.Shop.StockOf(it)
		if s.roadOpen() && (stock > int64(2*target) || (rainSoon && stock > int64(target))) {
			d.CitySell[it] = stock - int64(target)
		}
	}
	if rainSoon && s.roadOpen() {
		d.Reasons = append(d.Reasons, "The panchang says floods are coming. I sell my extra fish to the city and stock rice while the road is open.")
	} else if s.World.Flood {
		d.Reasons = append(d.Reasons, "The road is flooded. I am holding cash and buying carefully.")
	} else {
		d.Reasons = append(d.Reasons, "Clear skies in the panchang. Normal trade.")
	}
}

func decideLakshmi(s Situation, d *Decision) {
	season := s.World.Season
	cash := int64(s.Shop.Kauri)
	mango := s.Shop.StockOf("mango")
	chilli := s.Shop.StockOf("chilli")
	pickle := s.Shop.StockOf("pickle")
	for _, it := range s.Spec.Items {
		target, margin := 8, 1500
		switch {
		case it == "mango" && season == "garmi":
			target = 16
		case it == "pickle" && season == "tyohar":
			target = 6
		case it == "pickle":
			target = 4 // she makes her own; she will not overpay for anyone else's
		}
		d.Policies = append(d.Policies, market.Policy{TokenID: it, TargetStock: target, MarginBps: margin})
	}
	// She turns mangoes into pickle, buying chillies from the city when she must.
	if mango >= 6 && s.roadOpen() && cash > 600 && chilli < mango/2 {
		buy := mango/2 - chilli
		d.CityBuy["chilli"] = buy
		chilli += buy
	}
	craft := min(mango/2, chilli)
	if craft > 0 {
		d.Craft = min(craft, maxCityTrade)
		d.Reasons = append(d.Reasons, fmt.Sprintf("I am making %d jars of pickle from my mangoes.", d.Craft))
		pickle += d.Craft
	}
	keep := int64(pickleKeep)
	if season != "tyohar" {
		keep = 12
	}
	if s.roadOpen() && pickle > keep {
		d.CitySell["pickle"] = pickle - keep
		if season == "tyohar" {
			d.Reasons = append(d.Reasons, "Diwali is near and the city pays double for pickle.")
		} else {
			d.Reasons = append(d.Reasons, "My shelf of pickle is full, the rest goes to the city.")
		}
	}
	if season == "garmi" {
		d.Reasons = append(d.Reasons, "Mango season. I buy mangoes for pickle, but not at any price.")
	}
	if len(d.Reasons) == 0 {
		d.Reasons = append(d.Reasons, "Steady work: I keep a few jars of pickle on the shelf and wait for mango season.")
	}
}

// clampCity applies shared sense: cash crunches, the contract's trade limit,
// and never selling what the shop does not hold.
func clampCity(s Situation, d *Decision) {
	cash := int64(s.Shop.Kauri)
	if cash < cashCrunch && s.roadOpen() && s.Spec.Key != "meena" {
		for _, p := range d.Policies {
			if s.Spec.Key == "lakshmi" && p.TokenID == "mango" {
				continue
			}
			stock := s.Shop.StockOf(p.TokenID)
			if extra := stock - int64(p.TargetStock)/2; extra > d.CitySell[p.TokenID] {
				d.CitySell[p.TokenID] = extra
			}
		}
		d.Reasons = append(d.Reasons, "Cash is short, so I am selling stock to the city.")
	}
	if !s.roadOpen() {
		d.CitySell = map[string]int64{}
		d.CityBuy = map[string]int64{}
	}
	for k, v := range d.CitySell {
		// City buys and crafting run earlier in the same transaction.
		avail := s.Shop.StockOf(k) + d.CityBuy[k]
		switch k {
		case "pickle":
			avail += d.Craft
		case "mango":
			avail -= 2 * d.Craft
		case "chilli":
			avail -= d.Craft
		}
		if v > avail {
			v = avail
		}
		if v > maxCityTrade {
			v = maxCityTrade
		}
		if v <= 0 {
			delete(d.CitySell, k)
		} else {
			d.CitySell[k] = v
		}
	}
	// A shop never spends its reserve; a sensible one keeps clear of closing too.
	spend := cash - max(int64(s.Spec.Bounds.CashFloor), closeBelow) - 50
	if s.Spec.Key == "meena" {
		spend = cash - 20 // Meena is bold: she will spend her last kauri on stock
	}
	if s.Spec.CityBuyBeforeFloodOnly && !s.floodSoon(2) {
		spend = 0
	}
	for _, k := range SortedKeys(d.CityBuy) {
		v := min(d.CityBuy[k], maxCityTrade)
		unit := (s.World.CityPrice(k)*125 + 99) / 100
		if unit > 0 && v*unit > spend {
			v = spend / unit
		}
		if v <= 0 {
			delete(d.CityBuy, k)
			continue
		}
		d.CityBuy[k] = v
		spend -= v * unit
	}
}

func composeNote(s Situation, d Decision) string {
	note := strings.Join(d.Reasons, " ")
	if len(note) > 200 {
		note = note[:197]
		if i := strings.LastIndex(note, " "); i > 120 {
			note = note[:i]
		}
		note += "..."
	}
	return note
}

// SortedKeys returns map keys in a fixed order, for deterministic transactions.
func SortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
