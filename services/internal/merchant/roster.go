// Package merchant holds the four shopkeepers of the haat: who they are, what
// they trade, and (in persona.go) how each one decides.
package merchant

import "haat/internal/market"

// Spec describes a shop at the moment it opens.
type Spec struct {
	Key          string // sub-account name, e.g. "ramu" for ramu.<root>
	Name         string
	Temperament  string
	Items        []string
	OpeningKauri int64
	OpeningStock []int64
	// Bounds are the hard limits of the character. The contract enforces
	// them; the agent also clamps every decision to them before signing.
	Bounds market.Bounds
	// CityBuyBeforeFloodOnly: this shopkeeper buys from the city only when
	// the panchang shows a flood within two days.
	CityBuyBeforeFloodOnly bool
}

// Roster is the village's shopkeepers, in stall order.
var Roster = []Spec{
	{Key: "ramu", Name: "Ramu Kaka", Temperament: "cautious", Items: []string{"rice", "mango"}, OpeningKauri: 3000, OpeningStock: []int64{20, 20},
		Bounds: market.Bounds{MarginMin: 1800, MarginMax: 2500, TargetMax: 20, CashFloor: 1800}},
	{Key: "meena", Name: "Meena Didi", Temperament: "bold", Items: []string{"chilli", "pickle"}, OpeningKauri: 3000, OpeningStock: []int64{15, 3},
		Bounds: market.Bounds{MarginMin: 400, MarginMax: 1800, TargetMax: 60, CashFloor: 0}},
	{Key: "bashir", Name: "Bashir Bhai", Temperament: "reads the panchang", Items: []string{"fish", "rice"}, OpeningKauri: 2500, OpeningStock: []int64{20, 15},
		Bounds: market.Bounds{MarginMin: 1000, MarginMax: 2500, TargetMax: 25, CashFloor: 1250}, CityBuyBeforeFloodOnly: true},
	{Key: "lakshmi", Name: "Lakshmi Amma", Temperament: "craftswoman", Items: []string{"mango", "pickle"}, OpeningKauri: 2500, OpeningStock: []int64{15, 4},
		Bounds: market.Bounds{MarginMin: 1000, MarginMax: 2000, TargetMax: 30, CashFloor: 600}},
}

// ByKey finds a shopkeeper by sub-account name.
func ByKey(key string) (Spec, bool) {
	for _, s := range Roster {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}
