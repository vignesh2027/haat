package merchant

import (
	"strings"
	"testing"

	"haat/internal/market"
)

func shop(key string, kauri int64, stock map[string]int64) market.Merchant {
	spec, _ := ByKey(key)
	m := market.Merchant{AccountID: key + ".root.testnet", Name: spec.Name, Open: kauri >= closeBelow, Kauri: market.U128(kauri)}
	for item, n := range stock {
		m.Stock = append(m.Stock, market.Pair{Token: item, Amount: market.U128(n)})
	}
	for _, it := range spec.Items {
		m.Shelves = append(m.Shelves, market.Shelf{TokenID: it, Stock: market.U128(stock[it]), TargetStock: 20, MarginBps: 1000})
	}
	return m
}

func world(season string, flood bool, forecast ...bool) market.World {
	w := market.World{Season: season, Flood: flood}
	for i, f := range forecast {
		w.Forecast = append(w.Forecast, market.ForecastDay{Day: i + 1, Season: season, Flood: f})
	}
	for item, p := range map[string]int64{"mango": 48, "rice": 50, "fish": 70, "chilli": 80, "pickle": 300} {
		w.CityPrices = append(w.CityPrices, market.Pair{Token: item, Amount: market.U128(p)})
	}
	return w
}

func decide(key string, kauri int64, stock map[string]int64, w market.World) Decision {
	spec, _ := ByKey(key)
	return Decide(Situation{Spec: spec, Shop: shop(key, kauri, stock), World: w})
}

func TestRamuHoldsBackWhenCashIsLow(t *testing.T) {
	rich := decide("ramu", 3000, map[string]int64{"rice": 10, "mango": 10}, world("garmi", false))
	poor := decide("ramu", 1000, map[string]int64{"rice": 10, "mango": 10}, world("garmi", false))
	if rich.Policies[0].TargetStock <= poor.Policies[0].TargetStock {
		t.Fatalf("a poor Ramu should want less stock: rich %d, poor %d", rich.Policies[0].TargetStock, poor.Policies[0].TargetStock)
	}
	if rich.Policies[0].MarginBps != 2000 {
		t.Fatal("Ramu keeps a 20% margin")
	}
	if !strings.Contains(poor.Note, "aside") {
		t.Fatalf("note should explain the reserve: %q", poor.Note)
	}
}

func TestOverfullShelvesGoToTheCity(t *testing.T) {
	d := decide("ramu", 3000, map[string]int64{"rice": 60, "mango": 5}, world("garmi", false))
	if d.CitySell["rice"] != 45 {
		t.Fatalf("Ramu should send rice above target to the city, got %v", d.CitySell)
	}
	if _, ok := d.CitySell["mango"]; ok {
		t.Fatal("no mango to spare")
	}
}

func TestNoCityTradesOnFloodDays(t *testing.T) {
	for _, s := range Roster {
		spec := s
		d := Decide(Situation{Spec: spec, Shop: shop(spec.Key, 300, map[string]int64{"rice": 90, "mango": 90, "fish": 90, "chilli": 1, "pickle": 90}), World: world("barsaat", true)})
		if len(d.CitySell)+len(d.CityBuy) > 0 {
			t.Fatalf("%s traded with the city on a flood day: %+v", spec.Key, d)
		}
	}
}

func TestBashirReadsThePanchang(t *testing.T) {
	calm := decide("bashir", 2500, map[string]int64{"fish": 18, "rice": 12}, world("barsaat", false, false, false, false))
	rain := decide("bashir", 2500, map[string]int64{"fish": 18, "rice": 12}, world("barsaat", false, false, true, false))
	if rain.Policies[0].MarginBps <= calm.Policies[0].MarginBps {
		t.Fatal("Bashir should widen his margin before a flood")
	}
	if rain.CitySell["fish"] == 0 {
		t.Fatalf("Bashir should sell fish to the city before the flood: %+v", rain.CitySell)
	}
	if !strings.Contains(rain.Note, "panchang") {
		t.Fatalf("note: %q", rain.Note)
	}
}

func TestMeenaRestocksBoldly(t *testing.T) {
	d := decide("meena", 1500, map[string]int64{"chilli": 3, "pickle": 2}, world("garmi", false))
	if d.CityBuy["chilli"] != 12 {
		t.Fatalf("Meena should order 12 chillies, got %v", d.CityBuy)
	}
	if d.Policies[0].MarginBps != 600 {
		t.Fatal("Meena keeps a thin margin")
	}
	// Bold spending: with little cash she still spends nearly all of it.
	poor := decide("meena", 420, map[string]int64{"chilli": 2}, world("barsaat", false))
	if poor.CityBuy["chilli"] == 0 {
		t.Fatal("Meena spends her last kauri on chillies")
	}
}

func TestSensibleShopsNeverBuyThemselvesClosed(t *testing.T) {
	d := decide("lakshmi", 900, map[string]int64{"mango": 40}, world("garmi", false))
	spent := d.CityBuy["chilli"] * 100
	if 900-spent < closeBelow {
		t.Fatalf("Lakshmi spent %d of 900", spent)
	}
}

func TestLakshmiMakesPickleAndSellsAtDiwali(t *testing.T) {
	d := decide("lakshmi", 2500, map[string]int64{"mango": 10, "chilli": 5, "pickle": 6}, world("tyohar", false))
	if d.Craft != 5 {
		t.Fatalf("should craft 5 jars, got %d", d.Craft)
	}
	if d.CitySell["pickle"] != 6+5-pickleKeep {
		t.Fatalf("should sell pickle to the city at Diwali, got %v", d.CitySell)
	}
}

func TestDecisionsAreDeterministicAndNotesFit(t *testing.T) {
	for _, s := range Roster {
		a := Decide(Situation{Spec: s, Shop: shop(s.Key, 300, map[string]int64{"mango": 70, "rice": 70}), World: world("garmi", false, true)})
		b := Decide(Situation{Spec: s, Shop: shop(s.Key, 300, map[string]int64{"mango": 70, "rice": 70}), World: world("garmi", false, true)})
		if a.Note != b.Note || len(a.Policies) != len(b.Policies) {
			t.Fatalf("%s is not deterministic", s.Key)
		}
		if len(a.Note) > 200 || a.Note == "" {
			t.Fatalf("%s note length %d", s.Key, len(a.Note))
		}
		for _, p := range a.Policies {
			if p.TargetStock < 1 || p.TargetStock > 200 || p.MarginBps < 200 || p.MarginBps > 4000 {
				t.Fatalf("%s policy out of contract bounds: %+v", s.Key, p)
			}
		}
	}
}

func TestExplainBid(t *testing.T) {
	sh := market.Shelf{TokenID: "mango", Stock: 34, TargetStock: 20, MarginBps: 1000, Bid: 31}
	e := ExplainBid("Ramu Kaka", sh, 48)
	want := "I already have 34 mangoes and the city pays 48 for one, so I can offer you 31."
	if e.Headline != want {
		t.Fatalf("got %q", e.Headline)
	}
	if !strings.Contains(strings.Join(e.Lines, " "), "times 0.74") {
		t.Fatalf("stock factor missing: %v", e.Lines)
	}
	ask := market.U128(40)
	sh.Ask = &ask
	sh.RecentDemand = 5
	if a := ExplainAsk("Ramu Kaka", sh, 48); !strings.Contains(a.Headline, "Everyone wants mangoes") {
		t.Fatalf("got %q", a.Headline)
	}
}
