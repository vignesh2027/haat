package web

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"haat/internal/players"
)

// bucket is a token bucket: `rate` tokens per second, up to `burst`.
type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
}

func newLimiter(rate, burst float64) *limiter {
	return &limiter{buckets: map[string]*bucket{}, rate: rate, burst: burst}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) > 50_000 {
			l.buckets = map[string]*bucket{} // forget old visitors rather than grow forever
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limits groups the per-visitor limits.
type limits struct {
	startPerIP *limiter // new accounts per IP: 3 per hour
	actPerAcct *limiter // moves per account: 2 per second, burst 10
	actPerIP   *limiter // moves per IP: 5 per second, burst 25
	ffPerAcct  *limiter // fast-forward presses per account
}

func newLimits() *limits {
	return &limits{
		startPerIP: newLimiter(3.0/3600, 3),
		actPerAcct: newLimiter(2, 10),
		actPerIP:   newLimiter(5, 25),
		ffPerAcct:  newLimiter(1.0/300, 2),
	}
}

func (l *limits) allowAct(account, ip string) bool {
	return l.actPerAcct.allow(account) && l.actPerIP.allow(ip)
}

// clientIP is the visitor's address. Forwarded headers are only believed
// when the server is told it sits behind a proxy, and then the last hop the
// proxy added is used, since earlier entries can be forged by the client.
func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// accountPool keeps a few player accounts made ahead of time, so entering the
// haat does not wait for two testnet transactions.
type accountPool struct {
	ch chan *players.Player
}

func (s *Server) fillPool(ctx context.Context) {
	if s.pool == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if len(s.pool.ch) == cap(s.pool.ch) || !s.rootHealthy(ctx) {
			time.Sleep(5 * time.Second)
			continue
		}
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		p, err := players.Create(c, s.Root, s.Market.Contract, players.RandomName())
		cancel()
		if err != nil {
			log.Printf("account pool: %v", err)
			time.Sleep(15 * time.Second)
			continue
		}
		s.pool.ch <- p
	}
}

func (s *Server) rootHealthy(ctx context.Context) bool {
	bal, ok, err := s.Root.Client().AccountBalance(ctx, s.Root.ID)
	return err == nil && ok && bal.Cmp(s.MinRootNEAR) >= 0
}
