package web

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"haat/internal/market"
	"haat/internal/near"
)

const testSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestSessionSealRoundTripAndTamper(t *testing.T) {
	s, err := NewSealer(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	in := Session{Account: "kavya-1a2b.root.testnet", Play: "ed25519:abc", Recovery: "ed25519:def"}
	tok, err := s.Seal(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tok, "kavya") || strings.Contains(tok, "ed25519") {
		t.Fatal("sealed pass must not reveal its contents")
	}
	out, err := s.Open(tok)
	if err != nil || out != in {
		t.Fatalf("round trip failed: %+v %v", out, err)
	}
	b := []byte(tok)
	b[len(b)/2] ^= 1
	if _, err := s.Open(string(b)); err == nil {
		t.Fatal("tampered pass must be rejected")
	}
	other, _ := NewSealer(strings.Repeat("ab", 32))
	if _, err := other.Open(tok); err == nil {
		t.Fatal("a pass sealed with another secret must be rejected")
	}
	if _, err := NewSealer("short"); err == nil {
		t.Fatal("short secrets must be rejected")
	}
}

func TestFriendlyErrors(t *testing.T) {
	cases := map[string]string{
		"no energy left today, rest until tomorrow":         "You are tired",
		"not enough kauri: have 3, need 50":                 "Your purse is too light",
		"price moved: Meena Didi offers 300 kauri":          "Someone traded first",
		"Meena Didi's shop is closed until someone invests": "Meena Didi's shop is closed",
	}
	for in, want := range cases {
		got := friendlyError(&near.TxError{Message: in})
		if !strings.HasPrefix(got, want) {
			t.Errorf("%q gave %q", in, got)
		}
	}
	if got := friendlyError(errors.New("rpc http 503")); !strings.Contains(got, "slow") {
		t.Errorf("rpc error gave %q", got)
	}
}

func TestQuantityOptions(t *testing.T) {
	if got := options(0); len(got) != 0 {
		t.Fatal(got)
	}
	if got := options(3); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatal(got)
	}
	if got := options(80); len(got) != 3 || got[2] != 50 {
		t.Fatal(got)
	}
}

func u(v int64) *market.U128 { x := market.U128(v); return &x }

func fakeSnapshot() *snapshot {
	mid := "meena.root.testnet"
	w := market.World{Day: 9, Season: "barsaat", Flood: true, Players: 3,
		Forecast:   []market.ForecastDay{{Day: 10, Season: "barsaat", Flood: false}, {Day: 11, Season: "barsaat", Flood: true}, {Day: 12, Season: "barsaat"}},
		CityPrices: []market.Pair{{Token: "chilli", Amount: 112}, {Token: "pickle", Amount: 300}, {Token: "mango", Amount: 90}},
	}
	m := market.Merchant{AccountID: mid, Name: "Meena Didi", Open: false, Kauri: 120, Note: "My cash box is empty.",
		Shelves: []market.Shelf{
			{TokenID: "chilli", Stock: 30, TargetStock: 40, MarginBps: 600, Bid: 120, Ask: u(140)},
			{TokenID: "pickle", Stock: 0, TargetStock: 8, MarginBps: 600, Bid: 500},
		},
		Stock: []market.Pair{{Token: "chilli", Amount: 30}},
	}
	return &snapshot{World: w, Merchants: []market.Merchant{m}, Chronicle: []market.ChronicleEntry{
		{Day: 8, Kind: "flood"},
		{Day: 9, Kind: "shop_closed", MerchantID: &mid, Amount: u(120)},
	}}
}

func testServer(t *testing.T) *Server {
	s, err := New(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	s.Market = &market.Client{Contract: "market.root.testnet"}
	s.Explorer = "https://testnet.nearblocks.io"
	s.Network = "testnet"
	return s
}

func TestVillagePageRenders(t *testing.T) {
	s := testServer(t)
	snap := fakeSnapshot()
	pv := market.Player{AccountID: "kavya-1a2b.root.testnet", EnergyLeft: 4,
		Balances: []market.Pair{{Token: "kauri", Amount: 777}, {Token: "chilli", Amount: 7}, {Token: "mango", Amount: 4}}}
	data := s.basePage(snap, &flash{Kind: "ok", Text: "You gathered 3 fish at the river ghat."})
	data.Page, data.Title = "village", "test"
	data.Player = buildPlayer(Session{Account: pv.AccountID, Recovery: "x"}, pv, snap)
	data.Stalls = s.buildStalls(snap, &pv)
	rec := httptest.NewRecorder()
	s.render(rec, "village", data)
	body := rec.Body.String()
	for _, want := range []string{
		"Kavya", "777", "Meena Didi", "Closed", "Invest to reopen", "You gathered 3 fish",
		"Heavy rain, road flooded", "ran out of money and closed with 120 kauri", "Make 2", "Road flooded",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("village page is missing %q", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("a template value was rejected by html/template")
	}
	if strings.Count(body, "<script") != 1 {
		t.Error("the page should carry exactly one small inline script")
	}
}

func TestDashboardAndOtherPagesRender(t *testing.T) {
	s := testServer(t)
	snap := fakeSnapshot()
	var hist []market.DaySnapshot
	for d := 0; d < 6; d++ {
		hist = append(hist, market.DaySnapshot{Day: d, Flood: d == 3, Shops: []market.ShopSnap{{
			MerchantID: "meena.root.testnet", Kauri: market.U128(1500 - 250*d), Open: d < 5,
			Shelves: []market.ShelfSnap{{TokenID: "chilli", Stock: 10, Bid: market.U128(100 + d)}},
		}}})
	}
	dash := buildDashboard(snap, hist)
	if len(dash.Charts) != 2 || dash.Floods != 1 || dash.Closed != 1 {
		t.Fatalf("dashboard: %d charts, %d floods, %d closed", len(dash.Charts), dash.Floods, dash.Closed)
	}
	if !strings.HasPrefix(dash.Charts[0].Lines[0].Path, "M") || dash.Charts[0].Threshold == nil {
		t.Fatal("purse chart needs a path and the closing line")
	}
	for _, page := range []string{"market", "welcome", "ledger"} {
		data := s.basePage(snap, nil)
		data.Page, data.Title = page, "test"
		data.Dash = dash
		data.Stalls = s.buildStalls(snap, nil)
		data.Player = &playerVM{Name: "Kavya", Account: "kavya-1a2b.root.testnet", HasRecovery: true}
		rec := httptest.NewRecorder()
		s.render(rec, page, data)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "ZgotmplZ") {
			t.Fatalf("%s page failed to render: %d", page, rec.Code)
		}
	}
}

func TestChartLabelsDoNotOverlap(t *testing.T) {
	lines := []line{{EndY: 50}, {EndY: 52}, {EndY: 53}}
	spreadLabels(lines)
	if lines[1].EndY-lines[0].EndY < 13 || lines[2].EndY-lines[1].EndY < 13 {
		t.Fatalf("labels overlap: %+v", lines)
	}
}
