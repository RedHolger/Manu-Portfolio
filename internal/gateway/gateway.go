// Package gateway — request router with bounded fault injection (§4.2).
//
// The gateway records ONE completed client-facing request per logical
// operation (including upstream connection errors and deadline expiry), so an
// API crash cannot vanish from its own metrics. Stdlib only.
//
// result ∈ {success, client_error, server_error, timeout, transport_error}.
// Fixed route templates only; no run/request/reservation IDs in labels.
// Buckets: 0.005 0.01 0.025 0.05 0.1 0.3 0.5 1 2 5 +Inf (0.3 required).
package gateway

import (
	"hash/fnv"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Buckets is the fixed histogram boundary set (seconds).
var Buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.3, 0.5, 1, 2, 5}

// Fault kinds (application-layer simulations; labeled honestly in reports).
const (
	FaultDelay     = "gateway_delay"
	FaultConnFail  = "conn_fail"
	FaultDepOutage = "dependency_failure" // admin-driven 503 before upstream
	MaxFaultTTL    = 120 * time.Second
)

// Fault is a bounded injection with monotonic expiry.
type Fault struct {
	ID       string
	Kind     string
	Slot     string // stable|candidate|both
	Delay    time.Duration
	Fraction float64   // 0..1, seeded per logical operation ID
	Expires  time.Time // monotonic (time.Now-based)
}

// Config is an immutable routing snapshot (mutex-swapped, race-tested).
type Config struct {
	Version          uint64
	CandidatePercent int // 0..100
}

// Gateway routes and observes.
type Gateway struct {
	mu       sync.RWMutex
	cfg      Config
	faults   map[string]Fault
	upstream map[string]string // slot -> base URL

	muMet        sync.Mutex
	count        map[string]uint64   // key: slot|route|result
	sum          map[string]float64  // key: slot|route|result (seconds)
	bkt          map[string][]uint64 // key: slot|route|result (len Buckets+1)
	inflight     map[string]*int64
	activeFaults map[string]int64 // kind -> count (gauge snapshot)

	transport *http.Client
	now       func() time.Time
}

// New builds a gateway; upstream maps slot→base URL (e.g. stable→svc:8081).
func New(upstream map[string]string) *Gateway {
	return &Gateway{
		faults:       map[string]Fault{},
		upstream:     upstream,
		count:        map[string]uint64{},
		sum:          map[string]float64{},
		bkt:          map[string][]uint64{},
		inflight:     map[string]*int64{},
		activeFaults: map[string]int64{},
		transport:    &http.Client{Timeout: 5 * time.Second},
		now:          time.Now,
	}
}

// SetRouting applies a versioned update; stale version ⇒ conflict (false).
func (g *Gateway) SetRouting(expectedVersion uint64, percent int) (Config, bool) {
	if percent < 0 || percent > 100 {
		return Config{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cfg.Version != expectedVersion {
		return g.cfg, false
	}
	g.cfg = Config{Version: expectedVersion + 1, CandidatePercent: percent}
	return g.cfg, true
}

// State returns the routing version, weights, and unexpired faults.
func (g *Gateway) State() (Config, []Fault) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	now := g.now()
	var live []Fault
	for _, f := range g.faults {
		if now.Before(f.Expires) {
			live = append(live, f)
		}
	}
	return g.cfg, live
}

// PutFault installs an idempotent bounded fault; TTL must be ≤120s and cover
// the requested fault duration. Returns false on invalid input.
func (g *Gateway) PutFault(f Fault, ttl time.Duration) bool {
	if ttl <= 0 || ttl > MaxFaultTTL {
		return false
	}
	if f.Fraction < 0 || f.Fraction > 1 {
		return false
	}
	switch f.Kind {
	case FaultDelay, FaultConnFail, FaultDepOutage:
	default:
		return false
	}
	if f.Slot != "stable" && f.Slot != "candidate" && f.Slot != "both" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	f.Expires = g.now().Add(ttl)
	g.faults[f.ID] = f
	g.recountFaultsLocked()
	return true
}

// ClearFault removes one fault (idempotent); ClearAll is emergency-only.
func (g *Gateway) ClearFault(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.faults, id)
	g.recountFaultsLocked()
}

// ClearAll removes every fault (emergency lab cleanup only).
func (g *Gateway) ClearAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.faults = map[string]Fault{}
	g.recountFaultsLocked()
}

// Sweep expires faults (also checked per-request; sweeper is belt-and-braces).
func (g *Gateway) Sweep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for id, f := range g.faults {
		if !now.Before(f.Expires) {
			delete(g.faults, id)
		}
	}
	g.recountFaultsLocked()
}

func (g *Gateway) recountFaultsLocked() {
	g.activeFaults = map[string]int64{}
	now := g.now()
	for _, f := range g.faults {
		if now.Before(f.Expires) {
			g.activeFaults[f.Kind]++
		}
	}
}

// InitSeries pre-creates zero counters for every bounded label combination
// so that zero-error counts EXIST as series (§5.3). Missing series after
// init still means genuinely missing telemetry — presence/freshness checks
// stay strict. Call once at startup; restarts re-init (documented reset).
func (g *Gateway) InitSeries(slots, routes, results []string) {
	g.muMet.Lock()
	defer g.muMet.Unlock()
	for _, slot := range slots {
		for _, route := range routes {
			for _, result := range results {
				key := slot + "|" + route + "|" + result
				if _, ok := g.count[key]; !ok {
					g.count[key] = 0
					g.sum[key] = 0
					g.bkt[key] = make([]uint64, len(Buckets)+1)
				}
			}
		}
		if _, ok := g.inflight[slot]; !ok {
			g.inflight[slot] = new(int64)
		}
	}
}

// concurrent scheduling never changes which operations go to candidate.
// pickSlot hashes the logical operation ID for stable sampling across runs;
// concurrent scheduling never changes which operations go to candidate.
func (g *Gateway) pickSlot(opID string) string {
	g.mu.RLock()
	pct := g.cfg.CandidatePercent
	g.mu.RUnlock()
	if pct <= 0 {
		return "stable"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(opID))
	if int(h.Sum32()%100) < pct {
		return "candidate"
	}
	return "stable"
}

// faultFor returns the live fault affecting (slot, opID), if any.
func (g *Gateway) faultFor(slot, opID string) *Fault {
	g.mu.RLock()
	defer g.mu.RUnlock()
	now := g.now()
	for _, f := range g.faults {
		if !now.Before(f.Expires) {
			continue
		}
		if f.Slot != "both" && f.Slot != slot {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(opID + "\x00" + f.ID))
		if float64(h.Sum32()%1000)/1000.0 < f.Fraction {
			c := f
			return &c
		}
	}
	return nil
}

func resultClass(code int, err error, timeout bool) string {
	if timeout {
		return "timeout"
	}
	if err != nil {
		return "transport_error"
	}
	switch {
	case code < 400:
		return "success"
	case code < 500:
		return "client_error"
	default:
		return "server_error"
	}
}

// inflightPtr returns the gauge pointer for a slot (created under lock).
func (g *Gateway) inflightPtr(slot string) *int64 {
	g.muMet.Lock()
	defer g.muMet.Unlock()
	p, ok := g.inflight[slot]
	if !ok {
		p = new(int64)
		g.inflight[slot] = p
	}
	return p
}

func (g *Gateway) observe(slot, route, result string, d time.Duration) {
	key := slot + "|" + route + "|" + result
	secs := d.Seconds()
	g.muMet.Lock()
	defer g.muMet.Unlock()
	g.count[key]++
	g.sum[key] += secs
	b, ok := g.bkt[key]
	if !ok {
		b = make([]uint64, len(Buckets)+1)
	}
	i := 0
	for i < len(Buckets) && secs > Buckets[i] {
		i++
	}
	b[i]++
	g.bkt[key] = b
}

// ServeReserve proxies one logical operation and records exactly one sample.
// opID selects slot + fault deterministically; upstream deadline 800ms in the
// resilient profile (configurable per ReverseProxy use — default 5s client).
func (g *Gateway) ServeReserve(w http.ResponseWriter, r *http.Request, opID, route string) {
	slot := g.pickSlot(opID)
	gauge := g.inflightPtr(slot)
	atomic.AddInt64(gauge, 1)
	defer atomic.AddInt64(gauge, -1)
	start := g.now()

	if f := g.faultFor(slot, opID); f != nil {
		switch f.Kind {
		case FaultDelay:
			select {
			case <-time.After(f.Delay):
			case <-r.Context().Done():
				g.observe(slot, route, "timeout", g.now().Sub(start))
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
		case FaultConnFail:
			// Simulated pre-upstream failure: gateway-observed transport
			// error, surfaced as 502. NOT a real TCP reset (label honestly).
			g.observe(slot, route, "transport_error", g.now().Sub(start))
			w.WriteHeader(http.StatusBadGateway)
			return
		case FaultDepOutage:
			g.observe(slot, route, "server_error", g.now().Sub(start))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}

	base, ok := g.upstream[slot]
	if !ok {
		g.observe(slot, route, "transport_error", g.now().Sub(start))
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	// Proxy with per-request deadline; no silent retry against the other slot:
	// a candidate failure must stay visible.
	out, err := http.NewRequestWithContext(r.Context(), r.Method, base+r.URL.Path, r.Body)
	if err != nil {
		g.observe(slot, route, "transport_error", g.now().Sub(start))
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	for k, vv := range r.Header {
		for _, v := range vv {
			out.Header.Add(k, v)
		}
	}
	out.Header.Set("X-Slot", slot)
	resp, derr := g.transport.Do(out)
	if derr != nil {
		g.observe(slot, route, "transport_error", g.now().Sub(start))
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	g.observe(slot, route, resultClass(resp.StatusCode, nil, false), g.now().Sub(start))
}
