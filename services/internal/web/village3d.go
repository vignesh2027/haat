package web

import (
	"context"
	"net/http"
	"time"

	"haat/internal/market"
	"haat/internal/players"
)

// The 3D village reads the same snapshot as the pages, through the same view
// models, so it can never show a state the 2D village does not. It is a pure
// read: it does not touch the day clock and needs no session (a visitor sees
// the square; a player also sees their own lamps).

type sceneVM struct {
	Day         int             `json:"day"`
	Season      string          `json:"season"`
	SeasonLabel string          `json:"season_label"`
	Flood       bool            `json:"flood"`
	Forecast    []sceneForecast `json:"forecast"`
	Clock       sceneClock      `json:"clock"`
	FastForward sceneFF         `json:"fast_forward"`
	DiwaliIn    int             `json:"diwali_in"`
	Places      []scenePlace    `json:"places"`
	Shops       []sceneShop     `json:"shops"`
	Chronicle   []sceneChron    `json:"chronicle"`
	Player      *scenePlayer    `json:"player"`
}

type sceneForecast struct {
	Day   int    `json:"day"`
	Flood bool   `json:"flood"`
	Label string `json:"label"`
}

// sceneClock is the server's day clock: how far into the current game day the
// village is, so the 3D view can tint morning, noon, evening and night.
type sceneClock struct {
	Awake       bool    `json:"awake"`
	DayFraction float64 `json:"day_fraction"`
	DaySeconds  float64 `json:"day_seconds"`
}

type sceneFF struct {
	Running bool `json:"running"`
	Day     int  `json:"day"`
	Of      int  `json:"of"`
}

type scenePlace struct {
	Place  string `json:"place"`
	Name   string `json:"name"`
	Item   string `json:"item"`
	Yield  string `json:"yield"`
	Barren bool   `json:"barren"`
}

type sceneShop struct {
	Key          string       `json:"key"`
	Name         string       `json:"name"`
	Account      string       `json:"account"`
	Temperament  string       `json:"temperament"`
	Face         string       `json:"face"`
	Mood         string       `json:"mood"`
	Open         bool         `json:"open"`
	Reopened     bool         `json:"reopened"`
	Purse        int64        `json:"purse"`
	SharesIssued int64        `json:"shares_issued"`
	Note         string       `json:"note"`
	Shelves      []sceneShelf `json:"shelves"`
}

type sceneShelf struct {
	Token  string `json:"token"`
	Label  string `json:"label"`
	Emoji  string `json:"emoji"`
	Stock  int64  `json:"stock"`
	Bid    int64  `json:"bid"`
	Ask    int64  `json:"ask"`
	HasAsk bool   `json:"has_ask"`
}

type sceneChron struct {
	Day       int    `json:"day"`
	Emoji     string `json:"emoji"`
	Text      string `json:"text"`
	Important bool   `json:"important"`
}

type scenePlayer struct {
	Name    string `json:"name"`
	Rank    int    `json:"rank"`
	Of      int    `json:"of"`
	Rescues int    `json:"rescues"`
	Lamps   int    `json:"lamps"`
}

// villageJSON serves the state the 3D village draws.
func (s *Server) villageJSON(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The village ledger is not answering right now."})
		return
	}
	account := ""
	if sess, ok := s.readSession(r); ok {
		account = sess.Account
	}
	writeJSON(w, http.StatusOK, s.buildScene(snap, account))
}

func (s *Server) buildScene(snap *snapshot, account string) sceneVM {
	d := s.basePage(snap, nil)
	if account != "" {
		d.markYou(account)
	}
	v := sceneVM{
		Day: d.World.Day, Season: d.World.Season, SeasonLabel: d.Season, Flood: d.World.Flood,
		DiwaliIn: d.DiwaliIn, FastForward: sceneFF{Running: d.FF.Running, Day: d.FF.Day, Of: d.FF.Of},
		Forecast: []sceneForecast{}, Places: []scenePlace{}, Shops: []sceneShop{}, Chronicle: []sceneChron{},
	}
	if s.Keeper != nil {
		v.Clock = sceneClock{
			Awake:       s.Keeper.Awake(),
			DayFraction: dayFraction(s.Keeper.NextDayIn(), s.Keeper.DayLength),
			DaySeconds:  s.Keeper.DayLength.Seconds(),
		}
	} else {
		v.Clock = sceneClock{DayFraction: 0.35}
	}
	for _, f := range d.Forecast {
		v.Forecast = append(v.Forecast, sceneForecast{Day: f.Day, Flood: f.Flood, Label: f.Label})
	}
	for _, p := range d.Places {
		v.Places = append(v.Places, scenePlace{Place: p.Place, Name: p.Name, Item: p.Item, Yield: p.Yield, Barren: p.Barren})
	}
	reopened := reopenedShops(snap.Chronicle)
	for _, st := range s.buildStalls(snap, nil) {
		shop := sceneShop{
			Key: st.Key, Name: st.Name, Account: st.Account, Temperament: st.Temperament, Face: st.Face,
			Mood: st.Mood, Open: st.Open, Reopened: st.Open && reopened[st.Account], Purse: st.Purse,
			SharesIssued: st.SharesIssued, Note: st.Note, Shelves: []sceneShelf{},
		}
		if st.Voice != "" {
			shop.Note = st.Voice
		}
		for _, sh := range st.Shelves {
			shop.Shelves = append(shop.Shelves, sceneShelf{
				Token: sh.Token, Label: sh.Label, Emoji: sh.Emoji, Stock: sh.Stock, Bid: sh.Bid, Ask: sh.Ask, HasAsk: sh.HasAsk,
			})
		}
		v.Shops = append(v.Shops, shop)
	}
	for i, c := range d.Chronicle {
		if i == 4 {
			break
		}
		v.Chronicle = append(v.Chronicle, sceneChron{Day: c.Day, Emoji: c.Emoji, Text: c.Text, Important: c.Important})
	}
	if account != "" {
		for _, l := range d.Leaders {
			if l.You {
				v.Player = &scenePlayer{Name: l.Name, Rank: l.Rank, Of: len(d.Leaders), Rescues: l.Rescues, Lamps: min(l.Rescues, 5)}
			}
		}
		if v.Player == nil {
			v.Player = &scenePlayer{Name: players.DisplayName(account), Of: len(d.Leaders)}
		}
	}
	return v
}

// reopenedShops lists the shops whose latest open or close entry in the
// chronicle is a reopening: they were rescued and have not closed since.
func reopenedShops(chron []market.ChronicleEntry) map[string]bool {
	out := map[string]bool{}
	for _, c := range chron {
		if c.MerchantID == nil {
			continue
		}
		switch c.Kind {
		case "shop_reopened":
			out[*c.MerchantID] = true
		case "shop_closed", "shop_opened":
			out[*c.MerchantID] = false
		}
	}
	return out
}

// dayFraction is how far through the game day the clock is, from 0 at
// daybreak to 1 when the next day begins.
func dayFraction(left, length time.Duration) float64 {
	if length <= 0 {
		return 0
	}
	f := 1 - float64(left)/float64(length)
	return max(0, min(1, f))
}
