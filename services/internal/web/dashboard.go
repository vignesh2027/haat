package web

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"haat/internal/market"
)

// Charts are drawn on the server as inline SVG, so the dashboard needs no
// JavaScript and no chart library.

var shopColor = map[string]string{"ramu": "#2563EB", "meena": "#9F1239", "bashir": "#4C1D95", "lakshmi": "#0E7490"}

type dashboard struct {
	Days       int
	Charts     []chart
	Closed     int
	Floods     int
	Legend     []legendItem
	TotalKauri int64
}

type legendItem struct{ Name, Color string }

type chart struct {
	Title, Sub string
	W, H       int
	Lines      []line
	Floods     []band
	YTicks     []tick
	XTicks     []tick
	Threshold  *tick
}

type line struct {
	Name, Color, Path string
	EndX, EndY        float64
	Last              int64
	Dots              []dot
}

type dot struct{ X, Y float64 }

type band struct{ X, W float64 }

type tick struct {
	Pos   float64
	Label string
}

const (
	chartW, chartH         = 640, 200
	padL, padR, padT, padB = 44, 64, 12, 26
)

func buildDashboard(snap *snapshot, hist []market.DaySnapshot) *dashboard {
	d := &dashboard{Days: len(hist)}
	names := map[string]string{}
	for _, m := range snap.Merchants {
		names[m.AccountID] = m.Name
		key, _, _ := strings.Cut(m.AccountID, ".")
		d.Legend = append(d.Legend, legendItem{Name: m.Name, Color: shopColor[key]})
		d.TotalKauri += int64(m.Kauri)
		if !m.Open {
			d.Closed++
		}
	}
	for _, h := range hist {
		if h.Flood {
			d.Floods++
		}
	}
	if len(hist) == 0 {
		return d
	}
	// Purses.
	purse := map[string][]float64{}
	for _, h := range hist {
		for _, s := range h.Shops {
			purse[s.MerchantID] = append(purse[s.MerchantID], float64(s.Kauri))
		}
	}
	c := newChart("Shop purses", "Kauri in each cash box at the end of each day. Below the dashed line a shop closes.", hist, purse, names)
	c.Threshold = &tick{Pos: c.y(200), Label: "closes"}
	d.Charts = append(d.Charts, c.chart)

	// What shops pay for each item.
	for _, item := range []string{"mango", "rice", "fish", "chilli", "pickle"} {
		series := map[string][]float64{}
		for _, h := range hist {
			for _, s := range h.Shops {
				for _, sh := range s.Shelves {
					if sh.TokenID == item {
						series[s.MerchantID] = append(series[s.MerchantID], float64(sh.Bid))
					}
				}
			}
		}
		if len(series) == 0 {
			continue
		}
		cc := newChart(fmt.Sprintf("%s %s: what shops pay", itemEmoji[item], itemLabel[item]),
			"Price offered for the next unit a player sells, by shop.", hist, series, names)
		d.Charts = append(d.Charts, cc.chart)
	}
	return d
}

type scaler struct {
	chart
	minY, maxY float64
	n          int
}

func (s *scaler) x(i int) float64 {
	if s.n <= 1 {
		return padL
	}
	return padL + float64(i)*float64(chartW-padL-padR)/float64(s.n-1)
}

func (s *scaler) y(v float64) float64 {
	return padT + (s.maxY-v)*float64(chartH-padT-padB)/(s.maxY-s.minY)
}

func newChart(title, sub string, hist []market.DaySnapshot, series map[string][]float64, names map[string]string) *scaler {
	s := &scaler{chart: chart{Title: title, Sub: sub, W: chartW, H: chartH}, n: len(hist)}
	s.minY, s.maxY = 0, 1
	for _, vals := range series {
		for _, v := range vals {
			s.maxY = math.Max(s.maxY, v)
		}
	}
	s.maxY = niceCeil(s.maxY * 1.1)
	for i, h := range hist {
		if h.Flood {
			w := float64(chartW-padL-padR) / float64(max(s.n-1, 1))
			s.Floods = append(s.Floods, band{X: s.x(i) - w/2, W: w})
		}
	}
	for i := 0; i <= 4; i++ {
		v := s.maxY * float64(i) / 4
		s.YTicks = append(s.YTicks, tick{Pos: s.y(v), Label: fmt.Sprintf("%.0f", v)})
	}
	step := max(1, s.n/8)
	for i := 0; i < s.n; i += step {
		s.XTicks = append(s.XTicks, tick{Pos: s.x(i), Label: fmt.Sprintf("d%d", hist[i].Day)})
	}
	for _, id := range sortedIDs(series) {
		vals := series[id]
		key, _, _ := strings.Cut(id, ".")
		ln := line{Name: names[id], Color: shopColor[key]}
		var b strings.Builder
		off := s.n - len(vals) // shops that opened later start later
		for i, v := range vals {
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			x, y := s.x(i+off), s.y(v)
			fmt.Fprintf(&b, "%s%.1f %.1f ", cmd, x, y)
			if len(vals) <= 40 {
				ln.Dots = append(ln.Dots, dot{X: x, Y: y})
			}
			ln.EndX, ln.EndY, ln.Last = x, y, int64(v)
		}
		ln.Path = strings.TrimSpace(b.String())
		s.Lines = append(s.Lines, ln)
	}
	spreadLabels(s.Lines)
	return s
}

// spreadLabels moves end-of-line labels apart so they never overlap.
func spreadLabels(lines []line) {
	idx := make([]int, len(lines))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return lines[idx[a]].EndY < lines[idx[b]].EndY })
	for k := 1; k < len(idx); k++ {
		prev, cur := &lines[idx[k-1]], &lines[idx[k]]
		if cur.EndY-prev.EndY < 13 {
			cur.EndY = prev.EndY + 13
		}
	}
}

func sortedIDs(m map[string][]float64) []string {
	order := []string{"ramu", "meena", "bashir", "lakshmi"}
	var out []string
	for _, k := range order {
		for id := range m {
			if strings.HasPrefix(id, k+".") {
				out = append(out, id)
			}
		}
	}
	return out
}

func niceCeil(v float64) float64 {
	if v <= 10 {
		return 10
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*mag >= v {
			return m * mag
		}
	}
	return 10 * mag
}
