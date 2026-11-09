package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"haat/internal/market"
)

func TestSceneMirrorsTheSnapshot(t *testing.T) {
	s := testServer(t)
	snap := fakeSnapshot()
	v := s.buildScene(snap, "")
	if v.Day != 9 || v.Season != "barsaat" || !v.Flood {
		t.Fatalf("world: %+v", v)
	}
	if v.Player != nil {
		t.Fatal("a visitor has no player in the scene")
	}
	if len(v.Shops) != 1 {
		t.Fatalf("shops: %+v", v.Shops)
	}
	m := v.Shops[0]
	if m.Key != "meena" || m.Open || m.Reopened || m.Purse != 120 || m.Mood != "😞" {
		t.Fatalf("a closed shop must be drawn closed: %+v", m)
	}
	if len(m.Shelves) != 2 || m.Shelves[0].Stock != 30 || m.Shelves[0].Ask != 140 || !m.Shelves[0].HasAsk || m.Shelves[1].HasAsk {
		t.Fatalf("shelves must carry the contract's stock and prices: %+v", m.Shelves)
	}
	if len(v.Forecast) != 3 || !v.Forecast[1].Flood {
		t.Fatalf("forecast: %+v", v.Forecast)
	}
	if len(v.Places) != 4 {
		t.Fatalf("places: %+v", v.Places)
	}
	for _, p := range v.Places {
		// In the monsoon the grove and the chilli garden give nothing.
		if want := p.Place == "grove" || p.Place == "garden"; p.Barren != want {
			t.Errorf("%s barren = %v", p.Place, p.Barren)
		}
	}
	if len(v.Chronicle) == 0 || !strings.Contains(v.Chronicle[0].Text, "closed with 120 kauri") {
		t.Fatalf("chronicle: %+v", v.Chronicle)
	}
	// Empty lists are sent as [] so the page never has to guard against null.
	b, _ := json.Marshal(s.buildScene(&snapshot{World: market.World{Day: 1, Season: "garmi"}}, ""))
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	for _, k := range []string{"forecast", "places", "shops", "chronicle"} {
		if _, ok := raw[k].([]any); !ok {
			t.Errorf("%s should be a list, got %s", k, b)
		}
	}
}

func TestScenePlayerLamps(t *testing.T) {
	s := testServer(t)
	snap := fakeSnapshot()
	you := "kavya-1a2b.root.testnet"
	snap.Leaders = []market.LeaderRow{
		{AccountID: "ravi-9f9f.root.testnet", NetWorth: 900},
		{AccountID: you, NetWorth: 700, Rescues: 7},
		{AccountID: "fisher.root.testnet", NetWorth: 5000, Rescues: 2}, // a villager, not ranked
	}
	v := s.buildScene(snap, you)
	if v.Player == nil || v.Player.Name != "Kavya" || v.Player.Rank != 2 || v.Player.Of != 2 || v.Player.Rescues != 7 || v.Player.Lamps != 5 {
		t.Fatalf("player: %+v", v.Player)
	}
	// A brand new player who is not on the leaderboard yet has no lamps.
	v = s.buildScene(snap, "isha-0000.root.testnet")
	if v.Player == nil || v.Player.Lamps != 0 || v.Player.Rank != 0 || v.Player.Name != "Isha" {
		t.Fatalf("new player: %+v", v.Player)
	}
}

func TestReopenedShops(t *testing.T) {
	a, b := "meena.r", "ramu.r"
	who := "kavya-1.r"
	chron := []market.ChronicleEntry{
		{Kind: "shop_opened", MerchantID: &a},
		{Kind: "shop_closed", MerchantID: &a},
		{Kind: "shop_reopened", MerchantID: &a, AccountID: &who},
		{Kind: "invest", MerchantID: &b, AccountID: &who},
		{Kind: "shop_closed", MerchantID: &b},
		{Kind: "flood"},
	}
	got := reopenedShops(chron)
	if !got[a] || got[b] {
		t.Fatalf("got %v", got)
	}
	chron = append(chron, market.ChronicleEntry{Kind: "shop_closed", MerchantID: &a})
	if reopenedShops(chron)[a] {
		t.Fatal("a shop that closed again is no longer a rescued shop")
	}
	s := testServer(t)
	snap := fakeSnapshot()
	snap.Merchants[0].Open = true
	snap.Chronicle = append(snap.Chronicle, market.ChronicleEntry{Day: 10, Kind: "shop_reopened", MerchantID: &snap.Merchants[0].AccountID, AccountID: &who})
	if v := s.buildScene(snap, ""); !v.Shops[0].Reopened {
		t.Fatal("the scene should mark a rescued shop")
	}
}

func TestDayFraction(t *testing.T) {
	cases := []struct {
		left, length time.Duration
		want         float64
	}{
		{15 * time.Minute, 15 * time.Minute, 0},
		{0, 15 * time.Minute, 1},
		{5 * time.Minute, 20 * time.Minute, 0.75},
		{time.Hour, 15 * time.Minute, 0},
		{time.Minute, 0, 0},
	}
	for _, c := range cases {
		if got := dayFraction(c.left, c.length); got != c.want {
			t.Errorf("dayFraction(%v, %v) = %v, want %v", c.left, c.length, got, c.want)
		}
	}
}

func TestVillageJSONRoute(t *testing.T) {
	s := testServer(t)
	s.cache, s.cacheAt = fakeSnapshot(), time.Now()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/village.json", nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
	}
	var v sceneVM
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Day != 9 || len(v.Shops) != 1 || v.Shops[0].Open {
		t.Fatalf("decoded %+v", v)
	}
}

func TestStaticScriptsAreGzipped(t *testing.T) {
	s := testServer(t)
	h := s.Handler()
	want, err := assets.ReadFile("static/vendor/three-0.160.1/three.module.min.js")
	if err != nil {
		t.Fatal("the pinned Three.js build must be vendored:", err)
	}
	req := httptest.NewRequest("GET", "/static/vendor/three-0.160.1/three.module.min.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
	}
	if rec.Body.Len() > len(want)/2 {
		t.Fatalf("gzip saved too little: %d of %d bytes", rec.Body.Len(), len(want))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, want) {
		t.Fatal("gzipped body does not match the file")
	}
	// Without gzip, and for images, files are served as they are.
	for _, c := range []struct{ path, enc string }{
		{"/static/village3d.js", ""},
		{"/static/logo-512.png", "gzip"},
	} {
		req := httptest.NewRequest("GET", c.path, nil)
		if c.enc != "" {
			req.Header.Set("Accept-Encoding", c.enc)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Header().Get("Content-Encoding") != "" {
			t.Fatalf("%s: status %d, encoding %q", c.path, rec.Code, rec.Header().Get("Content-Encoding"))
		}
		if strings.HasSuffix(c.path, ".js") && rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("%s: plain copy needs Vary too", c.path)
		}
	}
	req = httptest.NewRequest("GET", "/static/nope.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing file gave %d", rec.Code)
	}
	for enc, want := range map[string]bool{
		"gzip": true, "deflate, gzip;q=0.5": true, "GZIP": true,
		"gzip;q=0": false, "gzip; q=0.000": false, "br": false, "": false,
	} {
		if got := acceptsGzip(&http.Request{Header: http.Header{"Accept-Encoding": {enc}}}); got != want {
			t.Errorf("acceptsGzip(%q) = %v", enc, got)
		}
	}
}

func TestVillagePageOffersThe3DView(t *testing.T) {
	s := testServer(t)
	snap := fakeSnapshot()
	pv := market.Player{AccountID: "kavya-1a2b.root.testnet", EnergyLeft: 4}
	data := s.basePage(snap, nil)
	data.Page, data.Title = "village", "test"
	data.Player = buildPlayer(Session{Account: pv.AccountID}, pv, snap)
	data.Stalls = s.buildStalls(snap, &pv)
	data.FF.Running = true
	rec := httptest.NewRecorder()
	s.render(rec, "village", data)
	body := rec.Body.String()
	for _, want := range []string{
		`href="#3d" data-v3d hidden`, `href="/#3d"`, `/static/village3d.js?v=` + village3dVersion,
		`<noscript><meta http-equiv="refresh" content="6"></noscript>`, `data-ff="1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("village page is missing %q", want)
		}
	}
	// The 3D script is loaded only on demand: no module script on the page.
	if strings.Contains(body, `type="module"`) || strings.Count(body, "<script") != 1 {
		t.Error("the page should still carry exactly one small inline script")
	}
	js, _ := assets.ReadFile("static/village3d.js")
	if !bytes.Contains(js, []byte("./vendor/three-0.160.1/three.module.min.js")) {
		t.Error("the 3D view must import the pinned, vendored Three.js")
	}
}
