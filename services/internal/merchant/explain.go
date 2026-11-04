package merchant

import (
	"fmt"

	"haat/internal/market"
)

// Item names as players see them.
var ItemNames = map[string]string{
	"mango": "mango", "rice": "rice", "fish": "fish", "chilli": "chilli", "pickle": "pickle",
	"kauri": "kauri",
}

// Plural returns the item name for n units.
func Plural(item string, n int64) string {
	name := ItemNames[item]
	if n == 1 {
		if item == "pickle" {
			return "jar of pickle"
		}
		return name
	}
	switch item {
	case "mango":
		return "mangoes"
	case "chilli":
		return "chillies"
	case "rice", "fish":
		return name
	case "pickle":
		return "jars of pickle"
	}
	return name + "s"
}

// StockFactor mirrors pricing.rs: 2T / (T + s), as a multiplier.
func StockFactor(stock int64, target int) float64 {
	t := float64(max(target, 1))
	return 2 * t / (t + float64(stock))
}

// DemandFactor mirrors pricing.rs: +5% per unit bought recently, at most +50%.
func DemandFactor(recent int64) float64 {
	return 1 + min(float64(recent)*0.05, 0.5)
}

// Explanation is the tooltip for one price.
type Explanation struct {
	Headline string   // one sentence in the shopkeeper's voice
	Lines    []string // the factors, in plain words
}

// ExplainBid explains what a shop pays a player for one unit.
func ExplainBid(name string, sh market.Shelf, city int64) Explanation {
	stock := int64(sh.Stock)
	e := Explanation{}
	switch {
	case int64(sh.Bid) == 0:
		e.Headline = fmt.Sprintf("I have %d %s already. I cannot use any more.", stock, Plural(sh.TokenID, stock))
	case stock > int64(sh.TargetStock):
		e.Headline = fmt.Sprintf("I already have %d %s and the city pays %d for one, so I can offer you %d.", stock, Plural(sh.TokenID, stock), city, sh.Bid)
	case stock < int64(sh.TargetStock):
		e.Headline = fmt.Sprintf("I am short of %s (%d of the %d I want), so I pay well: %d.", Plural(sh.TokenID, 2), stock, sh.TargetStock, sh.Bid)
	default:
		e.Headline = fmt.Sprintf("My stock is just right, so I pay close to the city price: %d.", sh.Bid)
	}
	e.Lines = factorLines(sh, city)
	e.Lines = append(e.Lines, fmt.Sprintf("Margin %s: pays %d for the next one.", pct(sh.MarginBps), sh.Bid))
	return e
}

// ExplainAsk explains what a shop charges a player for one unit.
func ExplainAsk(name string, sh market.Shelf, city int64) Explanation {
	stock := int64(sh.Stock)
	e := Explanation{}
	if sh.Ask == nil {
		e.Headline = fmt.Sprintf("Sold out of %s. Come back after someone brings some.", Plural(sh.TokenID, 2))
		e.Lines = factorLines(sh, city)
		return e
	}
	switch {
	case int64(sh.RecentDemand) >= 4:
		e.Headline = fmt.Sprintf("Everyone wants %s lately, so the price is up: %d.", Plural(sh.TokenID, 2), *sh.Ask)
	case stock < int64(sh.TargetStock):
		e.Headline = fmt.Sprintf("Only %d left on my shelf, so it costs %d.", stock, *sh.Ask)
	default:
		e.Headline = fmt.Sprintf("Plenty on the shelf. %d each.", *sh.Ask)
	}
	e.Lines = factorLines(sh, city)
	e.Lines = append(e.Lines, fmt.Sprintf("Margin %s: asks %d for the next one.", pct(sh.MarginBps), *sh.Ask))
	return e
}

func factorLines(sh market.Shelf, city int64) []string {
	stock := int64(sh.Stock)
	lines := []string{
		fmt.Sprintf("City price today: %d kauri.", city),
		fmt.Sprintf("Stock %d, wants %d: price times %.2f.", stock, sh.TargetStock, StockFactor(stock, sh.TargetStock)),
	}
	if r := int64(sh.RecentDemand); r > 0 {
		lines = append(lines, fmt.Sprintf("Bought by players lately: %d, price times %.2f.", r, DemandFactor(r)))
	}
	return lines
}

func pct(bps int) string {
	if bps%100 == 0 {
		return fmt.Sprintf("%d%%", bps/100)
	}
	return fmt.Sprintf("%.1f%%", float64(bps)/100)
}
