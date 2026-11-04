package merchant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"haat/internal/market"
)

// Brain lets a shopkeeper think with an open language model. Each review,
// the model sees the shop's situation, its temperament and what the rule
// book would do, and chooses the levers itself: target stock and margin per
// item, city trades, pickle to make, and a note in its own words.
//
// The model never touches prices directly. The contract turns the levers
// into prices with pricing.rs and enforces their bounds; the rule book
// clamps city trades to what the shop can afford and hold. If the model is
// unavailable, slow, over budget or returns nonsense, the rule book decides.
type Brain struct {
	APIKey string
	Model  string
	URL    string
	HTTP   *http.Client

	// TokensPerMinute and CallsPerDay keep inside the free tier.
	TokensPerMinute int
	CallsPerDay     int

	mu        sync.Mutex
	tokens    float64
	last      time.Time
	day       string
	callsUsed int
}

// NewBrain returns a brain using Groq's OpenAI-compatible API. An empty key disables it.
func NewBrain(apiKey, model string) *Brain {
	if model == "" {
		model = "openai/gpt-oss-20b"
	}
	return &Brain{
		APIKey: apiKey, Model: model,
		URL:             "https://api.groq.com/openai/v1/chat/completions",
		HTTP:            &http.Client{Timeout: 12 * time.Second},
		TokensPerMinute: 7000, CallsPerDay: 900,
	}
}

// Enabled reports whether a model is configured.
func (b *Brain) Enabled() bool { return b != nil && b.APIKey != "" }

// Thought records how a decision was made, for the stall's decision log.
type Thought struct {
	At        time.Time
	Source    string // "model" or "rules"
	Why       string // why the rule book decided, when it did
	Suggested string // what the rule book proposed
	Chosen    string // what was signed
	Note      string
	TxHash    string
}

const estTokensPerCall = 900

func (b *Brain) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	today := now.UTC().Format("2006-01-02")
	if b.day != today {
		b.day, b.callsUsed = today, 0
	}
	if b.CallsPerDay > 0 && b.callsUsed >= b.CallsPerDay {
		return false
	}
	if b.last.IsZero() {
		b.tokens = float64(b.TokensPerMinute)
	} else {
		b.tokens = min(float64(b.TokensPerMinute), b.tokens+now.Sub(b.last).Minutes()*float64(b.TokensPerMinute))
	}
	b.last = now
	if b.tokens < estTokensPerCall {
		return false
	}
	b.tokens -= estTokensPerCall
	b.callsUsed++
	return true
}

// ErrNoBudget means the model's free allowance is used up for now.
var ErrNoBudget = errors.New("model allowance used up for now")

type brainOut struct {
	Policies []market.Policy  `json:"policies"`
	CitySell map[string]int64 `json:"city_sell"`
	CityBuy  map[string]int64 `json:"city_buy"`
	Craft    int64            `json:"craft"`
	Note     string           `json:"note"`
}

// Think asks the model for a decision. rules is the rule book's proposal,
// shown to the model as advice. The result is validated and clamped.
func (b *Brain) Think(ctx context.Context, s Situation, rules Decision) (Decision, error) {
	if !b.Enabled() {
		return Decision{}, errors.New("no model configured")
	}
	if !b.take() {
		return Decision{}, ErrNoBudget
	}
	body, _ := json.Marshal(map[string]any{
		"model":            b.Model,
		"temperature":      0.5,
		"max_tokens":       700,
		"reasoning_effort": "low",
		"response_format":  map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt(s.Spec)},
			{"role": "user", "content": situationPrompt(s, rules)},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL, bytes.NewReader(body))
	if err != nil {
		return Decision{}, err
	}
	req.Header.Set("Authorization", "Bearer "+b.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return Decision{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Decision{}, fmt.Errorf("model http %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) == 0 {
		return Decision{}, errors.New("model returned no answer")
	}
	return ParseBrain(out.Choices[0].Message.Content, s)
}

// ParseBrain validates a model answer against the shop's situation: every
// item the shop trades gets a policy inside the contract's bounds, city
// trades and crafting are clamped by the same sense the rule book uses, and
// the note is cleaned to house style.
func ParseBrain(content string, s Situation) (Decision, error) {
	content = strings.TrimSpace(content)
	if i := strings.Index(content, "{"); i > 0 {
		content = content[i:]
	}
	if j := strings.LastIndex(content, "}"); j >= 0 {
		content = content[:j+1]
	}
	var o brainOut
	if err := json.Unmarshal([]byte(content), &o); err != nil {
		return Decision{}, fmt.Errorf("model answer is not JSON: %w", err)
	}
	d := Decision{CitySell: map[string]int64{}, CityBuy: map[string]int64{}}
	byItem := map[string]market.Policy{}
	for _, p := range o.Policies {
		byItem[p.TokenID] = p
	}
	for _, it := range s.Spec.Items {
		p, ok := byItem[it]
		if !ok {
			return Decision{}, fmt.Errorf("model gave no policy for %s", it)
		}
		p.TargetStock = clampInt(p.TargetStock, 1, 200)
		p.MarginBps = clampInt(p.MarginBps, 200, 4000)
		d.Policies = append(d.Policies, p)
	}
	for k, v := range o.CitySell {
		if market.PlaceFor(k) != "" || k == "pickle" {
			d.CitySell[k] = v
		}
	}
	for k, v := range o.CityBuy {
		if market.PlaceFor(k) != "" || k == "pickle" {
			d.CityBuy[k] = v
		}
	}
	mango, chilli := s.Shop.StockOf("mango")+d.CityBuy["mango"], s.Shop.StockOf("chilli")+d.CityBuy["chilli"]
	d.Craft = max(0, min(o.Craft, mango/2, chilli, maxCityTrade))
	clampCity(s, &d)
	note := Clean(o.Note)
	if note == "" {
		return Decision{}, errors.New("model gave no note")
	}
	if len(note) > 200 {
		note = note[:197] + "..."
	}
	d.Note = note
	d.Reasons = []string{note}
	return d, nil
}

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

func systemPrompt(spec Spec) string {
	traits := map[string]string{
		"ramu":    "You are cautious. You keep a wide margin and never let your cash box run low.",
		"meena":   "You are bold. You like thin margins and full shelves, and you take big bets on what the seasons will bring. Once investors rescue you, you become careful until your cash box is full again.",
		"bashir":  "You read the panchang (the almanac) and prepare for floods before they come: sell to the city while the road is open, hold cash when the rains come.",
		"lakshmi": "You are a craftswoman. You buy mangoes and chillies, make pickle (2 mangoes and 1 chilli make 1 jar), and sell pickle when the city pays most, near Diwali.",
	}[spec.Key]
	return fmt.Sprintf(`You are %s, a shopkeeper at a village market (haat) in India. %s
You run your own shop and your own cash box. Each review you choose your levers:
- target_stock per item (1 to 200): how much you want to hold. Below target you pay players more; above it you pay less.
- margin_bps per item (200 to 4000): your spread. You ask fair price plus margin and bid fair price minus margin.
- city_sell / city_buy: the city cart buys at 80%% of the city price and sells at 125%%, and never comes on flood days.
- craft: jars of pickle to make.
The contract sets every price from your levers, stock and recent demand. If your cash falls below 200 kauri your shop closes until someone invests.
Reply with JSON only: {"policies":[{"token_id":"...","target_stock":N,"margin_bps":N}],"city_sell":{"item":N},"city_buy":{"item":N},"craft":N,"note":"..."}
The note is what you would tell a customer at your stall: one or two short first-person sentences in warm plain English about what you are doing and why (the weather, the season, your stock, your cash, your investors). At most 180 characters. Do not use the words target, margin or bps, and no dashes, arrows or emoji.`, spec.Name, traits)
}

func situationPrompt(s Situation, rules Decision) string {
	w := s.World
	var b strings.Builder
	fmt.Fprintf(&b, "Day %d, season %s, road %s.", w.Day, w.Season, map[bool]string{true: "FLOODED today", false: "open"}[w.Flood])
	b.WriteString(" Panchang:")
	for _, f := range w.Forecast {
		fmt.Fprintf(&b, " day %d %s%s;", f.Day, f.Season, map[bool]string{true: " FLOOD", false: ""}[f.Flood])
	}
	b.WriteString("\nCity prices:")
	for _, p := range w.CityPrices {
		fmt.Fprintf(&b, " %s %d;", p.Token, p.Amount)
	}
	fmt.Fprintf(&b, "\nYour cash: %d kauri (opened with %d). Investors' shares: %d. Shop is %s.",
		s.Shop.Kauri, s.Spec.OpeningKauri, s.Shop.SharesIssued, map[bool]string{true: "open", false: "CLOSED"}[s.Shop.Open])
	b.WriteString("\nYour shelves:")
	for _, sh := range s.Shop.Shelves {
		ask := "sold out"
		if sh.Ask != nil {
			ask = fmt.Sprint(int64(*sh.Ask))
		}
		fmt.Fprintf(&b, "\n- %s: stock %d, target %d, margin %d bps, you bid %d, you ask %s, bought by players lately %d",
			sh.TokenID, sh.Stock, sh.TargetStock, sh.MarginBps, sh.Bid, ask, sh.RecentDemand)
	}
	var other []string
	for _, p := range s.Shop.Stock {
		if _, ok := s.Shop.Shelf(p.Token); !ok {
			other = append(other, fmt.Sprintf("%s %d", p.Token, p.Amount))
		}
	}
	if len(other) > 0 {
		fmt.Fprintf(&b, "\nOther stock: %s.", strings.Join(other, ", "))
	}
	fmt.Fprintf(&b, "\nYour rule book suggests: %s. Rule book reason: %s", Summary(rules), strings.Join(rules.Reasons, " "))
	b.WriteString("\nDecide now.")
	return b.String()
}

// Summary describes a decision's levers and trades in one line.
func Summary(d Decision) string {
	var parts []string
	for _, p := range d.Policies {
		parts = append(parts, fmt.Sprintf("%s target %d margin %d%%", p.TokenID, p.TargetStock, p.MarginBps/100))
	}
	for _, k := range sortedKeys(d.CitySell) {
		parts = append(parts, fmt.Sprintf("sell %d %s to the city", d.CitySell[k], k))
	}
	for _, k := range sortedKeys(d.CityBuy) {
		parts = append(parts, fmt.Sprintf("buy %d %s from the city", d.CityBuy[k], k))
	}
	if d.Craft > 0 {
		parts = append(parts, fmt.Sprintf("make %d pickle", d.Craft))
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]int64) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
