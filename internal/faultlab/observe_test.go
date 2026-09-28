package faultlab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedMetrics serves counters that grow per scrape: total += 100,
// failed += failPerScrape. stepAdvances fake time between scrapes is
// impossible over HTTP — ratios use real 1s-spaced scrapes in slow tests,
// so unit tests drive history directly via Scrape + crafted bodies.
func TestParseCountersSplits(t *testing.T) {
	exp := `# HELP x
lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="success"} 90
lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="server_error"} 7
lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="timeout"} 2
lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="transport_error"} 1
lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="client_error"} 50
lab_requests_total{service="reservations",slot="candidate",route="/v1/reservations",result="success"} 5
lab_request_duration_seconds_sum{service="reservations",slot="stable"} 1
`
	total, failed, found := parseCounters(exp, "stable")
	if !found || total != 150 || failed != 10 {
		t.Fatalf("total=%v failed=%v found=%v, want 150/10/true", total, failed, found)
	}
	if _, _, found := parseCounters(exp, "missing"); found {
		t.Fatal("unknown slot must not be found")
	}
}

func TestScrapeFailureIsError(t *testing.T) {
	o := NewObserver("http://127.0.0.1:1/metrics", "stable") // refused
	o.Client.Timeout = 2 * time.Second
	if _, err := o.Scrape(context.Background()); err == nil {
		t.Fatal("unreachable metrics must error (telemetry loss)")
	}
}

func TestScrapeNoSeriesIsError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# nothing here\n"))
	}))
	defer s.Close()
	o := NewObserver(s.URL, "stable")
	if _, err := o.Scrape(context.Background()); err == nil {
		t.Fatal("absent series must error, not yield zeros")
	}
}

func TestAbortTrippedOnSustainedFailure(t *testing.T) {
	var n atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := n.Add(1)
		// Every scrape: +100 total, +60 failed (60% failure).
		fmt.Fprintf(w, `lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="success"} %d
lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="server_error"} %d
`, 40*k, 60*k)
	}))
	defer s.Close()
	o := NewObserver(s.URL, "stable")
	ctx := context.Background()
	for i := 0; i < 12; i++ { // 12s of history at 1s spacing
		if _, err := o.Scrape(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1050 * time.Millisecond)
	}
	if !o.AbortTripped(0.50, 2, 5*time.Second) {
		t.Fatal("60% sustained failure must trip the 50-percent double-window abort")
	}
	if o.AbortTripped(0.99, 2, 5*time.Second) {
		t.Fatal("99% threshold must not trip on 60% failure")
	}
	if r, ok := o.WindowRatio(5 * time.Second); !ok || r < 0.59 || r > 0.61 {
		t.Fatalf("window ratio=%v ok=%v, want ~0.60", r, ok)
	}
}

func TestAbortNeedsConsecutiveWindows(t *testing.T) {
	o := &Observer{maxKeep: 10 * time.Minute}
	now := time.Now()
	// One bad 5s window followed by clean traffic: no trip.
	o.hist = []Sample{
		{At: now.Add(-12 * time.Second), Total: 0, Failed: 0},
		{At: now.Add(-7 * time.Second), Total: 500, Failed: 400},
		{At: now.Add(-2 * time.Second), Total: 1000, Failed: 400},
		{At: now, Total: 1500, Failed: 400},
	}
	if o.AbortTripped(0.50, 2, 5*time.Second) {
		t.Fatal("single bad window must not trip consecutive-2 abort")
	}
}
