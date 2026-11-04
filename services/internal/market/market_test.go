package market

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeContractViews(t *testing.T) {
	raw := `{"account_id":"kavya.t","registered":true,"energy_left":7,
	  "balances":[["kauri","780"],["mango","2"]],
	  "holdings":[{"merchant_id":"meena.t","shares":"125","pending_dividends":"35","stake_bps":"1111"}]}`
	var p Player
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if p.Balance("kauri") != 780 || p.Balance("mango") != 2 || p.Balance("fish") != 0 {
		t.Fatalf("balances: %+v", p.Balances)
	}
	if p.Holdings[0].PendingDividends != 35 {
		t.Fatal("holding not decoded")
	}
	var q Quote
	if err := json.Unmarshal([]byte(`{"total":null,"units":[],"amount":"3"}`), &q); err != nil || q.Total != nil || q.Amount != 3 {
		t.Fatalf("quote: %+v %v", q, err)
	}
}

func TestPlayEncodesMovesAsTheContractExpects(t *testing.T) {
	fc := Play(MoveGather("grove"), MoveSell("ramu.t", "mango", 3, 100))
	if fc.Method != "play" || fc.Gas == 0 {
		t.Fatal("play call")
	}
	s := string(fc.Args)
	for _, want := range []string{`{"gather":{"place":"grove"}}`, `"sell":{`, `"amount":"3"`, `"min_total":"100"`} {
		if !strings.Contains(s, want) {
			t.Errorf("args %s missing %s", s, want)
		}
	}
}
