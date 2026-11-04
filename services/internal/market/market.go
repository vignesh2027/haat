// Package market is a typed client for the Haat market contract.
package market

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	"haat/internal/near"
)

// U128 is a decimal-string u128 as NEAR contracts serialise it. Game amounts fit in int64.
type U128 int64

// UnmarshalJSON accepts "123" or 123.
func (u *U128) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		var n int64
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		*u = U128(n)
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	*u = U128(n)
	return err
}

// MarshalJSON writes a decimal string.
func (u U128) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.FormatInt(int64(u), 10)) }

// Pair is a (token, amount) tuple, serialised as a two-element array.
type Pair struct {
	Token  string
	Amount U128
}

// UnmarshalJSON reads ["token", "amount"].
func (p *Pair) UnmarshalJSON(b []byte) error {
	var raw [2]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[0], &p.Token); err != nil {
		return err
	}
	return json.Unmarshal(raw[1], &p.Amount)
}

type ForecastDay struct {
	Day    int    `json:"day"`
	Season string `json:"season"`
	Flood  bool   `json:"flood"`
}

type World struct {
	Day        int           `json:"day"`
	Season     string        `json:"season"`
	Flood      bool          `json:"flood"`
	Forecast   []ForecastDay `json:"forecast"`
	CityPrices []Pair        `json:"city_prices"`
	Players    int           `json:"players"`
	OwnerID    string        `json:"owner_id"`
}

// CityPrice returns today's city price of an item.
func (w World) CityPrice(token string) int64 {
	for _, p := range w.CityPrices {
		if p.Token == token {
			return int64(p.Amount)
		}
	}
	return 0
}

type Shelf struct {
	TokenID      string `json:"token_id"`
	Stock        U128   `json:"stock"`
	TargetStock  int    `json:"target_stock"`
	MarginBps    int    `json:"margin_bps"`
	RecentDemand U128   `json:"recent_demand"`
	Ask          *U128  `json:"ask"`
	Bid          U128   `json:"bid"`
}

type Merchant struct {
	AccountID    string  `json:"account_id"`
	Name         string  `json:"name"`
	Open         bool    `json:"open"`
	Kauri        U128    `json:"kauri"`
	Value        U128    `json:"value"`
	SharesIssued U128    `json:"shares_issued"`
	DividendPool U128    `json:"dividend_pool"`
	Note         string  `json:"note"`
	NoteDay      int     `json:"note_day"`
	SettledToday bool    `json:"settled_today"`
	Bounds       Bounds  `json:"bounds"`
	Shelves      []Shelf `json:"shelves"`
	Stock        []Pair  `json:"stock"`
}

// StockOf returns how many units of token the shop holds.
func (m Merchant) StockOf(token string) int64 {
	for _, p := range m.Stock {
		if p.Token == token {
			return int64(p.Amount)
		}
	}
	return 0
}

// Shelf returns the shelf for a token, if the shop trades it.
func (m Merchant) Shelf(token string) (Shelf, bool) {
	for _, s := range m.Shelves {
		if s.TokenID == token {
			return s, true
		}
	}
	return Shelf{}, false
}

type Quote struct {
	MerchantID      string `json:"merchant_id"`
	TokenID         string `json:"token_id"`
	Side            string `json:"side"`
	Amount          U128   `json:"amount"`
	Total           *U128  `json:"total"`
	Units           []U128 `json:"units"`
	CityPrice       U128   `json:"city_price"`
	Stock           U128   `json:"stock"`
	TargetStock     int    `json:"target_stock"`
	MarginBps       int    `json:"margin_bps"`
	RecentDemand    U128   `json:"recent_demand"`
	StockFactorBps  U128   `json:"stock_factor_bps"`
	DemandFactorBps U128   `json:"demand_factor_bps"`
	MerchantKauri   U128   `json:"merchant_kauri"`
	Spendable       U128   `json:"spendable"`
	Open            bool   `json:"open"`
}

type Holding struct {
	MerchantID       string `json:"merchant_id"`
	Shares           U128   `json:"shares"`
	PendingDividends U128   `json:"pending_dividends"`
	StakeBps         U128   `json:"stake_bps"`
}

type Player struct {
	AccountID  string    `json:"account_id"`
	Registered bool      `json:"registered"`
	EnergyLeft int       `json:"energy_left"`
	Balances   []Pair    `json:"balances"`
	Holdings   []Holding `json:"holdings"`
}

// Balance returns the player's balance of a token.
func (p Player) Balance(token string) int64 {
	for _, b := range p.Balances {
		if b.Token == token {
			return int64(b.Amount)
		}
	}
	return 0
}

type ShelfSnap struct {
	TokenID string `json:"token_id"`
	Stock   U128   `json:"stock"`
	Ask     *U128  `json:"ask"`
	Bid     U128   `json:"bid"`
}

type ShopSnap struct {
	MerchantID string      `json:"merchant_id"`
	Kauri      U128        `json:"kauri"`
	Open       bool        `json:"open"`
	Shelves    []ShelfSnap `json:"shelves"`
}

type DaySnapshot struct {
	Day   int        `json:"day"`
	Flood bool       `json:"flood"`
	Shops []ShopSnap `json:"shops"`
}

type ChronicleEntry struct {
	Day        int     `json:"day"`
	Kind       string  `json:"kind"`
	MerchantID *string `json:"merchant_id"`
	AccountID  *string `json:"account_id"`
	Amount     *U128   `json:"amount"`
}

// Policy is one shopkeeper lever setting.
type Policy struct {
	TokenID     string `json:"token_id"`
	TargetStock int    `json:"target_stock"`
	MarginBps   int    `json:"margin_bps"`
}

// Client reads the market contract.
type Client struct {
	RPC      *near.Client
	Contract string
}

func (c *Client) World(ctx context.Context) (w World, err error) {
	err = c.RPC.View(ctx, c.Contract, "get_world", nil, &w)
	return
}

func (c *Client) Merchants(ctx context.Context) (m []Merchant, err error) {
	err = c.RPC.View(ctx, c.Contract, "get_merchants", nil, &m)
	return
}

func (c *Client) Quote(ctx context.Context, merchant, token, side string, amount int64) (q Quote, err error) {
	err = c.RPC.View(ctx, c.Contract, "quote", map[string]any{
		"merchant_id": merchant, "token_id": token, "side": side, "amount": U128(amount),
	}, &q)
	return
}

func (c *Client) Player(ctx context.Context, account string) (p Player, err error) {
	err = c.RPC.View(ctx, c.Contract, "get_player", map[string]any{"account_id": account}, &p)
	return
}

func (c *Client) History(ctx context.Context, fromDay, limit int) (h []DaySnapshot, err error) {
	err = c.RPC.View(ctx, c.Contract, "history", map[string]any{"from_day": fromDay, "limit": limit}, &h)
	return
}

func (c *Client) Today(ctx context.Context) (d DaySnapshot, err error) {
	err = c.RPC.View(ctx, c.Contract, "today", nil, &d)
	return
}

func (c *Client) Chronicle(ctx context.Context, from, limit int) (e []ChronicleEntry, err error) {
	err = c.RPC.View(ctx, c.Contract, "chronicle", map[string]any{"from_index": from, "limit": limit}, &e)
	return
}

func (c *Client) ChronicleLen(ctx context.Context) (n int, err error) {
	err = c.RPC.View(ctx, c.Contract, "chronicle_len", nil, &n)
	return
}

// Action builders. Every game method takes no deposit, so a function-call
// access key can sign them and the relayer can pay for them.

const gas = 30

func Gather(place string) near.FunctionCall {
	return near.CallArgs("gather", map[string]any{"place": place}, gas)
}

func Craft(recipe string, amount int64) near.FunctionCall {
	return near.CallArgs("craft", map[string]any{"recipe": recipe, "amount": U128(amount)}, gas)
}

func Sell(merchant, token string, amount, minTotal int64) near.FunctionCall {
	return near.CallArgs("sell", map[string]any{"merchant_id": merchant, "token_id": token, "amount": U128(amount), "min_total": U128(minTotal)}, gas)
}

func Buy(merchant, token string, amount, maxTotal int64) near.FunctionCall {
	return near.CallArgs("buy", map[string]any{"merchant_id": merchant, "token_id": token, "amount": U128(amount), "max_total": U128(maxTotal)}, gas)
}

func Invest(merchant string, amount int64) near.FunctionCall {
	return near.CallArgs("invest", map[string]any{"merchant_id": merchant, "amount": U128(amount)}, gas)
}

func ClaimDividends(merchant string) near.FunctionCall {
	return near.CallArgs("claim_dividends", map[string]any{"merchant_id": merchant}, gas)
}

func SetPolicies(p []Policy, note string) near.FunctionCall {
	return near.CallArgs("set_policies", map[string]any{"policies": p, "note": note}, gas)
}

func CitySell(token string, amount int64) near.FunctionCall {
	return near.CallArgs("city_sell", map[string]any{"token_id": token, "amount": U128(amount)}, gas)
}

func CityBuy(token string, amount int64) near.FunctionCall {
	return near.CallArgs("city_buy", map[string]any{"token_id": token, "amount": U128(amount)}, gas)
}

func SettleDay() near.FunctionCall { return near.CallArgs("settle_day", map[string]any{}, gas) }

func AdvanceDay() near.FunctionCall { return near.CallArgs("advance_day", map[string]any{}, 100) }

func RegisterPlayer(account string) near.FunctionCall {
	return near.CallArgs("register_player", map[string]any{"account_id": account}, gas)
}

// Bounds are the limits of a shopkeeper's character, enforced by the contract.
type Bounds struct {
	MarginMin int  `json:"margin_min"`
	MarginMax int  `json:"margin_max"`
	TargetMax int  `json:"target_max"`
	CashFloor U128 `json:"cash_floor"`
}

// Spendable is the cash a shop will spend: its purse above its reserve.
func (m Merchant) Spendable() int64 { return max(0, int64(m.Kauri)-int64(m.Bounds.CashFloor)) }

func SetBounds(account string, b Bounds) near.FunctionCall {
	return near.CallArgs("set_bounds", map[string]any{"account_id": account, "bounds": b}, gas)
}

func AddMerchant(account, name string, items []string, kauri int64, stock []int64, b Bounds) near.FunctionCall {
	s := make([]U128, len(stock))
	for i, v := range stock {
		s[i] = U128(v)
	}
	return near.CallArgs("add_merchant", map[string]any{
		"account_id": account, "name": name, "items": items, "opening_kauri": U128(kauri), "opening_stock": s,
		"bounds": b,
	}, gas)
}

func Init(owner string, seed uint64) near.FunctionCall {
	return near.CallArgs("new", map[string]any{"owner_id": owner, "world_seed": seed}, gas)
}

// Move is one player move for the contract's `play` method.
type Move map[string]map[string]any

func MoveGather(place string) Move { return Move{"gather": {"place": place}} }

func MoveCraft(recipe string, amount int64) Move {
	return Move{"craft": {"recipe": recipe, "amount": U128(amount)}}
}

func MoveSell(merchant, token string, amount, minTotal int64) Move {
	return Move{"sell": {"merchant_id": merchant, "token_id": token, "amount": U128(amount), "min_total": U128(minTotal)}}
}

func MoveBuy(merchant, token string, amount, maxTotal int64) Move {
	return Move{"buy": {"merchant_id": merchant, "token_id": token, "amount": U128(amount), "max_total": U128(maxTotal)}}
}

func MoveInvest(merchant string, amount int64) Move {
	return Move{"invest": {"merchant_id": merchant, "amount": U128(amount)}}
}

func MoveClaim(merchant string) Move { return Move{"claim_dividends": {"merchant_id": merchant}} }

// Play runs up to 10 moves in one call. Players sign with a function-call
// access key, which may only carry one action, so every player transaction is a Play.
func Play(moves ...Move) near.FunctionCall {
	g := uint64(20 + 15*len(moves))
	if g > 250 {
		g = 250
	}
	return near.CallArgs("play", map[string]any{"moves": moves}, g)
}

// Places and their items, mirroring contract/src/world.rs.
var Places = []struct{ Place, Item string }{
	{"grove", "mango"}, {"field", "rice"}, {"river", "fish"}, {"garden", "chilli"},
}

// PlaceFor returns the place that yields an item.
func PlaceFor(item string) string {
	for _, p := range Places {
		if p.Item == item {
			return p.Place
		}
	}
	return ""
}

// BaseYield is the guaranteed yield of one gathering trip by season, mirroring
// base_yield in contract/src/world.rs. A lucky trip gives one more.
func BaseYield(season, item string) int64 {
	t := map[string]map[string]int64{
		"garmi":   {"mango": 3, "rice": 1, "fish": 1, "chilli": 2},
		"barsaat": {"mango": 0, "rice": 1, "fish": 3, "chilli": 0},
		"tyohar":  {"mango": 1, "rice": 3, "fish": 2, "chilli": 2},
	}
	return t[season][item]
}

// LeaderRow is one player's standing.
type LeaderRow struct {
	AccountID string `json:"account_id"`
	Kauri     U128   `json:"kauri"`
	NetWorth  U128   `json:"net_worth"`
	Rescues   int    `json:"rescues"`
}

// Leaderboard reads every player's standing, sorted by net worth, richest first.
func (c *Client) Leaderboard(ctx context.Context) ([]LeaderRow, error) {
	var all []LeaderRow
	for from := 0; ; from += 100 {
		var page []LeaderRow
		if err := c.RPC.View(ctx, c.Contract, "leaderboard", map[string]any{"from_index": from, "limit": 100}, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 100 || from > 5000 {
			break
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].NetWorth > all[j].NetWorth })
	return all, nil
}

// DaysToDiwali is how many game days remain until the festival day, the last
// day of the festival season (day 20 of each 21-day year).
func DaysToDiwali(day int) int { return ((20-day%21)%21 + 21) % 21 }
