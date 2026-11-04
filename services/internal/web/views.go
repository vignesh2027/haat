package web

import (
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"haat/internal/market"
	"haat/internal/merchant"
	"haat/internal/players"
	"haat/internal/world"
)

type pageData struct {
	Title     string
	Page      string
	Flash     *flash
	World     market.World
	Season    string
	Weather   string
	NextDay   string
	Awake     bool
	Network   string
	Contract  string
	Explorer  string
	Player    *playerVM
	Stalls    []stallVM
	Places    []placeVM
	Forecast  []forecastVM
	Feed      []feedVM
	Chronicle []chronVM
	Reveal    *revealVM
	Dash      *dashboard
	VoiceOn   bool
	FF        world.FastForward
	FFLeft    int
	Pending   int
	Leaders   []leaderVM
	YourRank  int
	DiwaliIn  int
}

type leaderVM struct {
	Account       string
	Rank          int
	Name          string
	NetWorth      int64
	Rescues       int
	You, Villager bool
}

type playerVM struct {
	Name, Account string
	Purse         int64
	Energy        int
	EnergyPips    []bool
	Basket        []itemCount
	Holdings      []holdingVM
	HasRecovery   bool
	CanCraft      int64
	NetWorth      int64
}

type itemCount struct {
	Token, Label, Emoji string
	Count, Value        int64
}

type holdingVM struct {
	MerchantID, Name string
	Shares, Pending  int64
	Stake            string
}

type stallVM struct {
	Key, Name, Temperament, Face, Account string
	Open                                  bool
	Purse, Value, SharesIssued            int64
	Note, Voice                           string
	Mood                                  string
	Shelves                               []shelfVM
	Thoughts                              []thoughtVM
	GasNEAR                               string
}

type thoughtVM struct {
	When, Source, Note, Suggested, Chosen, Why, Tx string
	Model                                          bool
}

type shelfVM struct {
	Token, Label, Emoji string
	Stock, Bid          int64
	Ask                 int64
	HasAsk              bool
	BidTip, AskTip      merchant.Explanation
	Have                int64 // how many the player holds
	SellOptions         []int64
	BuyOptions          []int64
}

type placeVM struct {
	Place, Name, Emoji, Item, ItemEmoji, Yield string
	Barren                                     bool
}

type forecastVM struct {
	Day           int
	Label, Emoji  string
	Flood, Change bool
}

type feedVM struct {
	When, Who, Text, Tx string
	Failed              bool
}

type chronVM struct {
	Day       int
	Emoji     string
	Text      string
	Important bool
}

type revealVM struct {
	Secret, Public, FileName string
	DataURL                  template.URL
}

var itemEmoji = map[string]string{"mango": "🥭", "rice": "🌾", "fish": "🐟", "chilli": "🌶️", "pickle": "🫙", "kauri": "🐚"}
var itemLabel = map[string]string{"mango": "Mango", "rice": "Rice", "fish": "Fish", "chilli": "Chilli", "pickle": "Pickle"}
var itemHindi = map[string]string{"mango": "aam", "rice": "chawal", "fish": "machli", "chilli": "mirchi", "pickle": "achaar"}
var faces = map[string]string{"ramu": "👴🏽", "meena": "👩🏽", "bashir": "🧔🏽", "lakshmi": "👵🏽"}

var places = []struct{ Place, Name, Emoji, Item string }{
	{"grove", "Mango grove", "🌳", "mango"},
	{"field", "Paddy field", "🌱", "rice"},
	{"river", "River ghat", "🛶", "fish"},
	{"garden", "Chilli garden", "🪴", "chilli"},
}

func placeItem(place string) string {
	for _, p := range places {
		if p.Place == place {
			return p.Item
		}
	}
	return ""
}

func placeName(place string) string {
	for _, p := range places {
		if p.Place == place {
			return strings.ToLower(p.Name)
		}
	}
	return place
}

func seasonEmoji(s string) string {
	switch s {
	case "garmi":
		return "☀️"
	case "barsaat":
		return "🌧️"
	case "tyohar":
		return "🪔"
	}
	return ""
}

func (s *Server) basePage(snap *snapshot, f *flash) pageData {
	w := snap.World
	d := pageData{
		Flash: f, World: w, Season: world.SeasonLabel(w.Season), Weather: seasonEmoji(w.Season),
		Network: s.Network, Contract: s.Market.Contract, Explorer: strings.TrimRight(s.Explorer, "/"),
		VoiceOn: s.Voice.Enabled(),
	}
	if w.Flood {
		d.Weather = "⛈️"
	}
	if s.Keeper != nil {
		d.FF, d.FFLeft = s.Keeper.FastForwardState(), s.Keeper.FastForwardsLeft()
		d.Awake = s.Keeper.Awake()
		mins := int(math.Ceil(s.Keeper.NextDayIn().Minutes()))
		d.NextDay = fmt.Sprintf("%d min", max(mins, 1))
		for _, it := range s.Keeper.Feed(10) {
			d.Feed = append(d.Feed, feedVM{When: ago(it.At), Who: it.Who, Text: it.Text, Tx: it.TxHash, Failed: it.Failed})
		}
	}
	for _, p := range places {
		y := market.BaseYield(w.Season, p.Item)
		pv := placeVM{Place: p.Place, Name: p.Name, Emoji: p.Emoji, Item: p.Item, ItemEmoji: itemEmoji[p.Item]}
		if y == 0 {
			pv.Barren, pv.Yield = true, "nothing this season"
		} else {
			pv.Yield = fmt.Sprintf("%d or %d per trip", y, y+1)
		}
		d.Places = append(d.Places, pv)
	}
	prev := w.Season
	for _, f := range w.Forecast {
		fv := forecastVM{Day: f.Day, Flood: f.Flood, Change: f.Season != prev}
		prev = f.Season
		switch {
		case f.Flood:
			fv.Emoji, fv.Label = "⛈️", "Heavy rain, road flooded"
		case f.Season == "barsaat":
			fv.Emoji, fv.Label = "🌦️", "Showers, road open"
		case f.Season == "tyohar":
			fv.Emoji, fv.Label = "🪔", "Festival days, road open"
		default:
			fv.Emoji, fv.Label = "☀️", "Hot and dry, road open"
		}
		if fv.Change {
			fv.Label = world.SeasonLabel(f.Season) + " begins. " + fv.Label
		}
		d.Forecast = append(d.Forecast, fv)
	}
	d.DiwaliIn = market.DaysToDiwali(w.Day)
	// Villagers keep the market moving but are not in the running: they
	// gather every day whether anyone plays or not.
	for _, l := range snap.Leaders {
		head, _, _ := strings.Cut(l.AccountID, ".")
		if !strings.Contains(head, "-") {
			continue
		}
		d.Leaders = append(d.Leaders, leaderVM{
			Account: l.AccountID, Rank: len(d.Leaders) + 1, Name: players.DisplayName(l.AccountID),
			NetWorth: int64(l.NetWorth), Rescues: l.Rescues,
		})
	}
	names := map[string]string{}
	for _, m := range snap.Merchants {
		names[m.AccountID] = m.Name
	}
	// Newest first; daily dividends are left to the feed so that bankruptcies,
	// rescues, floods and seasons stay visible.
	for i := len(snap.Chronicle) - 1; i >= 0 && len(d.Chronicle) < 12; i-- {
		if snap.Chronicle[i].Kind == "dividend" {
			continue
		}
		if c, ok := chronicleLine(snap.Chronicle[i], names); ok {
			d.Chronicle = append(d.Chronicle, c)
		}
	}
	return d
}

func chronicleLine(c market.ChronicleEntry, names map[string]string) (chronVM, bool) {
	shop := ""
	if c.MerchantID != nil {
		shop = names[*c.MerchantID]
	}
	who := ""
	if c.AccountID != nil {
		who = players.DisplayName(*c.AccountID)
	}
	amt := int64(0)
	if c.Amount != nil {
		amt = int64(*c.Amount)
	}
	v := chronVM{Day: c.Day}
	switch c.Kind {
	case "shop_opened":
		v.Emoji, v.Text = "🏪", shop+" opened a stall."
	case "shop_closed":
		v.Emoji, v.Text, v.Important = "🔒", fmt.Sprintf("%s ran out of money and closed with %d kauri.", shop, amt), true
	case "shop_reopened":
		v.Emoji, v.Text, v.Important = "🔓", shop+" opened again.", true
		if who != "" {
			v.Text = fmt.Sprintf("%s reopened thanks to %s.", shop, who)
		}
	case "invest":
		v.Emoji, v.Text = "🤝", fmt.Sprintf("%s invested %d kauri in %s.", who, amt, shop)
	case "dividend":
		v.Emoji, v.Text = "💰", fmt.Sprintf("%s paid %d kauri to investors.", shop, amt)
	case "flood":
		v.Emoji, v.Text, v.Important = "⛈️", "Floods. The city cart cannot reach the village.", true
	case "road_open":
		v.Emoji, v.Text = "🛤️", "The water went down and the road is open again."
	case "season_garmi", "season_barsaat", "season_tyohar":
		s := strings.TrimPrefix(c.Kind, "season_")
		v.Emoji, v.Text = seasonEmoji(s), world.SeasonLabel(s)+" begins."
	default:
		return v, false
	}
	return v, true
}

// markYou flags the player's own row and records their rank.
func (d *pageData) markYou(account string) {
	for i := range d.Leaders {
		if d.Leaders[i].Account == account {
			d.Leaders[i].You = true
			d.YourRank = d.Leaders[i].Rank
		}
	}
}

// TopLeaders is the first n rows, plus the player's own row if it is lower.
func (d pageData) TopLeaders(n int) []leaderVM {
	var out []leaderVM
	for i, l := range d.Leaders {
		if i < n || l.You {
			out = append(out, l)
		}
	}
	return out
}

func buildPlayer(sess Session, pv market.Player, snap *snapshot) *playerVM {
	p := &playerVM{
		Name: players.DisplayName(sess.Account), Account: sess.Account,
		Purse: pv.Balance("kauri"), Energy: pv.EnergyLeft, HasRecovery: sess.Recovery != "",
	}
	for i := 0; i < 10; i++ {
		p.EnergyPips = append(p.EnergyPips, i < pv.EnergyLeft)
	}
	p.NetWorth = p.Purse
	for _, t := range []string{"mango", "rice", "fish", "chilli", "pickle"} {
		n := pv.Balance(t)
		val := n * snap.World.CityPrice(t)
		p.NetWorth += val
		p.Basket = append(p.Basket, itemCount{Token: t, Label: itemLabel[t], Emoji: itemEmoji[t], Count: n, Value: val})
	}
	p.CanCraft = min(pv.Balance("mango")/2, pv.Balance("chilli"))
	names := map[string]string{}
	for _, m := range snap.Merchants {
		names[m.AccountID] = m.Name
	}
	for _, h := range pv.Holdings {
		p.Holdings = append(p.Holdings, holdingVM{
			MerchantID: h.MerchantID, Name: names[h.MerchantID], Shares: int64(h.Shares),
			Pending: int64(h.PendingDividends), Stake: fmt.Sprintf("%.1f%%", float64(h.StakeBps)/100),
		})
	}
	return p
}

func (s *Server) buildStalls(snap *snapshot, pv *market.Player) []stallVM {
	var out []stallVM
	for _, m := range snap.Merchants {
		key, _, _ := strings.Cut(m.AccountID, ".")
		spec, _ := merchant.ByKey(key)
		st := stallVM{
			Key: key, Name: m.Name, Temperament: spec.Temperament, Face: faces[key], Account: m.AccountID,
			Open: m.Open, Purse: int64(m.Kauri), Value: int64(m.Value), SharesIssued: int64(m.SharesIssued),
			Note: m.Note,
		}
		if m.Note != "" {
			st.Voice = s.Voice.Line(spec, m.Note)
		}
		if s.Keeper != nil {
			for _, a := range s.Keeper.Agents {
				if a.Account.ID != m.AccountID {
					continue
				}
				for _, t := range a.Thoughts(3) {
					tv := thoughtVM{When: ago(t.At), Note: t.Note, Suggested: t.Suggested, Chosen: t.Chosen, Why: t.Why, Tx: t.TxHash, Model: t.Source == "model"}
					tv.Source = "rule book"
					if tv.Model {
						tv.Source = "thought it through"
					}
					st.Thoughts = append(st.Thoughts, tv)
				}
			}
		}
		switch {
		case !m.Open:
			st.Mood = "😞"
		case int64(m.Kauri) < 600:
			st.Mood = "😟"
		case int64(m.Kauri) > spec.OpeningKauri:
			st.Mood = "😄"
		default:
			st.Mood = "🙂"
		}
		for _, sh := range m.Shelves {
			city := snap.World.CityPrice(sh.TokenID)
			sv := shelfVM{
				Token: sh.TokenID, Label: itemLabel[sh.TokenID], Emoji: itemEmoji[sh.TokenID],
				Stock: int64(sh.Stock), Bid: int64(sh.Bid),
				BidTip: merchant.ExplainBid(m.Name, sh, city), AskTip: merchant.ExplainAsk(m.Name, sh, city),
			}
			if sh.Ask != nil {
				sv.Ask, sv.HasAsk = int64(*sh.Ask), true
			}
			if pv != nil {
				sv.Have = pv.Balance(sh.TokenID)
				sv.SellOptions = options(sv.Have)
				sv.BuyOptions = options(min(sv.Stock, 50))
				if len(sv.BuyOptions) > 2 {
					sv.BuyOptions = sv.BuyOptions[:2]
				}
			}
			st.Shelves = append(st.Shelves, sv)
		}
		out = append(out, st)
	}
	return out
}

// options are the quantity buttons: 1, 5 and all (at most 50).
func options(have int64) []int64 {
	have = min(have, 50)
	var o []int64
	for _, n := range []int64{1, 5} {
		if n < have {
			o = append(o, n)
		}
	}
	if have > 0 {
		o = append(o, have)
	}
	return o
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
}

func funcs(s *Server) template.FuncMap {
	return template.FuncMap{
		"emoji":  func(t string) string { return itemEmoji[t] },
		"label":  func(t string) string { return itemLabel[t] },
		"hindi":  func(t string) string { return itemHindi[t] },
		"plural": merchant.Plural,
		"tx": func(hash string) string {
			return strings.TrimRight(s.Explorer, "/") + "/txns/" + hash
		},
		"acct": func(id string) string {
			return strings.TrimRight(s.Explorer, "/") + "/address/" + id
		},
		"num": func(v int64) string {
			str := fmt.Sprint(v)
			if len(str) <= 3 {
				return str
			}
			var b strings.Builder
			pre := len(str) % 3
			if pre > 0 {
				b.WriteString(str[:pre])
			}
			for i := pre; i < len(str); i += 3 {
				if b.Len() > 0 {
					b.WriteByte(',')
				}
				b.WriteString(str[i : i+3])
			}
			return b.String()
		},
		"isLocal": func() bool { return s.Network == "localnet" },
		"lamps":   func(n int) []struct{} { return make([]struct{}, min(n, 5)) },
		"css":     func() string { return "/static/haat.css?v=" + cssVersion },
		"ffpct": func(f world.FastForward) int {
			if f.Of == 0 {
				return 0
			}
			return (f.Day - 1) * 100 / f.Of
		},
		"ffsecs": func(f world.FastForward) int { return int(f.Took.Seconds()) },
	}
}
