package web

import (
	"context"
	"net/http/httptest"
	"testing"

	"haat/internal/market"
)

func TestLimiterBurstThenRefuse(t *testing.T) {
	l := newLimiter(0.0001, 3)
	for i := 0; i < 3; i++ {
		if !l.allow("ip") {
			t.Fatal("burst should be allowed")
		}
	}
	if l.allow("ip") {
		t.Fatal("fourth call should be refused")
	}
	if !l.allow("other") {
		t.Fatal("other visitors have their own bucket")
	}
}

func TestClientIPTrustsProxyOnlyWhenTold(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 9.9.9.9")
	if s.clientIP(r) != "10.0.0.1" {
		t.Fatal("forwarded headers must be ignored by default")
	}
	s.TrustProxy = true
	if s.clientIP(r) != "9.9.9.9" {
		t.Fatalf("behind a proxy, use the hop it added, got %s", s.clientIP(r))
	}
}

func TestMoveQueueReservesAndBatches(t *testing.T) {
	q := newMoveQueue()
	sess := Session{Account: "kavya.t"}
	for i := 0; i < 12; i++ {
		q.push(sess, &queuedMove{kind: "gather", mv: market.MoveGather("grove")})
	}
	q.push(sess, &queuedMove{kind: "sell", token: "mango", amount: 4})
	r := q.reserved("kavya.t")
	if r.energy != 12 || r.items["mango"] != 4 {
		t.Fatalf("reservation %+v", r)
	}
	batch := q.next()["kavya.t"]
	if len(batch) != 10 {
		t.Fatalf("a batch holds at most 10 moves, got %d", len(batch))
	}
	if len(q.next()) != 0 {
		t.Fatal("no second batch while one is in flight")
	}
	q.finish("kavya.t", []doneItem{{text: "ok"}}, nil)
	n, done, _ := q.status("kavya.t", true)
	if n != 3 || len(done) != 1 {
		t.Fatalf("pending %d done %v", n, done)
	}
	if _, done, _ := q.status("kavya.t", true); len(done) != 0 {
		t.Fatal("results are handed over once")
	}
}

func TestGasBudgetStopsAtTheLimit(t *testing.T) {
	g := &gasBudget{Limit: 2}
	s := &Server{}
	ctx := context.Background()
	if g.spend(ctx, s) != nil || g.spend(ctx, s) != nil {
		t.Fatal("first two allowed")
	}
	if g.spend(ctx, s) != errBudget {
		t.Fatal("third refused")
	}
}

func TestSummariseAddsUpTrips(t *testing.T) {
	got := summarise([]doneItem{
		{text: "a", gathered: "mango", n: 3}, {text: "b", gathered: "mango", n: 4},
		{text: "c", gathered: "fish", n: 2}, {text: "Ramu Kaka paid you 90 kauri for 3 mangoes."},
	})
	want := "You gathered 7 mangoes in 2 trips and 2 fish. Ramu Kaka paid you 90 kauri for 3 mangoes."
	if got != want {
		t.Fatalf("got %q", got)
	}
}
