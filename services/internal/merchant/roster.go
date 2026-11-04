// Package merchant holds the four shopkeepers of the haat: who they are, what
// they trade, and (in persona.go) how each one decides.
package merchant

// Spec describes a shop at the moment it opens.
type Spec struct {
	Key          string // sub-account name, e.g. "ramu" for ramu.<root>
	Name         string
	Temperament  string
	Items        []string
	OpeningKauri int64
	OpeningStock []int64
}

// Roster is the village's shopkeepers, in stall order.
var Roster = []Spec{
	{Key: "ramu", Name: "Ramu Kaka", Temperament: "cautious", Items: []string{"rice", "mango"}, OpeningKauri: 3000, OpeningStock: []int64{20, 20}},
	{Key: "meena", Name: "Meena Didi", Temperament: "bold", Items: []string{"chilli", "pickle"}, OpeningKauri: 3000, OpeningStock: []int64{15, 3}},
	{Key: "bashir", Name: "Bashir Bhai", Temperament: "reads the panchang", Items: []string{"fish", "rice"}, OpeningKauri: 2500, OpeningStock: []int64{20, 15}},
	{Key: "lakshmi", Name: "Lakshmi Amma", Temperament: "craftswoman", Items: []string{"mango", "pickle"}, OpeningKauri: 2500, OpeningStock: []int64{15, 4}},
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
