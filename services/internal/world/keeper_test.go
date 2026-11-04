package world

import (
	"testing"
	"time"

	"haat/internal/market"
)

func TestClockSleepsWhenNobodyPlays(t *testing.T) {
	k := &Keeper{DayLength: time.Minute, IdleAfter: 50 * time.Millisecond}
	if k.Awake() {
		t.Fatal("a new village is asleep")
	}
	k.Touch()
	if !k.Awake() || k.NextDayIn() > time.Minute || k.NextDayIn() < 50*time.Second {
		t.Fatalf("touch should wake the village and start the day timer, next day in %s", k.NextDayIn())
	}
	time.Sleep(80 * time.Millisecond)
	if k.Awake() {
		t.Fatal("the village should fall asleep when idle")
	}
}

func TestFeedIsNewestFirstAndBounded(t *testing.T) {
	k := &Keeper{}
	for i := 0; i < 250; i++ {
		k.Record(FeedItem{Who: "x", Text: string(rune('a' + i%26))})
	}
	f := k.Feed(3)
	if len(f) != 3 || f[0].Text != string(rune('a'+249%26)) {
		t.Fatalf("feed: %+v", f)
	}
	if len(k.feed) != 200 {
		t.Fatalf("feed should keep 200 items, has %d", len(k.feed))
	}
}

func TestSeasonLabels(t *testing.T) {
	if SeasonLabel("barsaat") != "Barsaat, the monsoon" {
		t.Fatal(SeasonLabel("barsaat"))
	}
}

func testView() View {
	u := func(v int64) *market.U128 { x := market.U128(v); return &x }
	shops := []market.Merchant{
		{AccountID: "ramu.r", Name: "Ramu Kaka", Open: true, Kauri: 3000, Shelves: []market.Shelf{
			{TokenID: "mango", Stock: 20, Bid: 40, Ask: u(60)}, {TokenID: "rice", Stock: 20, Bid: 30, Ask: u(50)}}},
		{AccountID: "lakshmi.r", Name: "Lakshmi Amma", Open: true, Kauri: 2000, Shelves: []market.Shelf{
			{TokenID: "mango", Stock: 5, Bid: 70, Ask: u(90)}, {TokenID: "pickle", Stock: 3, Bid: 300, Ask: u(400)}}},
		{AccountID: "meena.r", Name: "Meena Didi", Open: false, Kauri: 50, Shelves: []market.Shelf{
			{TokenID: "chilli", Stock: 5, Bid: 120, Ask: u(140)}, {TokenID: "pickle", Stock: 1, Bid: 500, Ask: u(600)}}},
	}
	w := market.World{Day: 3, Season: "garmi"}
	me := market.Player{EnergyLeft: 10, Balances: []market.Pair{{Token: "kauri", Amount: 900}, {Token: "mango", Amount: 3}, {Token: "chilli", Amount: 2}}}
	return View{World: w, Shops: shops, Me: me, ClosedBefore: map[string]bool{"meena.r": true}, Day: 3}
}

func TestVillagerPlans(t *testing.T) {
	v := testView()
	for _, r := range VillagerRoster {
		p := PlanDay(r.Habit, v)
		if len(p.Moves) == 0 || len(p.Moves) > 10 {
			t.Fatalf("%s planned %d moves", r.Handle, len(p.Moves))
		}
		for _, m := range p.Moves {
			if s, ok := m["sell"]; ok && s["merchant_id"] == "meena.r" {
				t.Fatalf("%s tried to sell to a closed shop", r.Handle)
			}
		}
	}
	gauri := PlanDay("mango picker", v)
	last := gauri.Moves[len(gauri.Moves)-2:]
	if last[0]["sell"]["merchant_id"] != "lakshmi.r" {
		t.Fatalf("the best bid for mangoes is Lakshmi's: %+v", last)
	}
	kishan := PlanDay("investor", v)
	if _, ok := kishan.Moves[0]["invest"]; !ok || kishan.Moves[0]["invest"]["merchant_id"] != "meena.r" {
		t.Fatalf("the investor should rescue a shop closed since yesterday: %+v", kishan.Moves[0])
	}
	v.ClosedBefore = map[string]bool{}
	if _, ok := PlanDay("investor", v).Moves[0]["invest"]; ok {
		t.Fatal("the investor waits a day so a player can rescue the shop first")
	}
}
