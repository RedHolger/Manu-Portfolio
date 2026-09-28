// observe.go — F2 workload observation by scraping gateway exposition.
// The abort decision uses windowed failure ratios computed from cumulative
// counters; scrape failures are ERRORS (missing telemetry triggers cleanup,
// never silent continuation).
package faultlab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sample is one scraped observation for the target slot.
type Sample struct {
	At     time.Time
	Total  float64
	Failed float64 // server_error + timeout + transport_error
}

// Observer polls gateway /metrics and keeps a bounded history.
type Observer struct {
	MetricsURL string
	Slot       string
	Client     *http.Client
	mu         sync.Mutex
	hist       []Sample
	maxKeep    time.Duration
}

// NewObserver scrapes metricsURL for slot, retaining 10 minutes.
func NewObserver(metricsURL, slot string) *Observer {
	return &Observer{MetricsURL: metricsURL, Slot: slot,
		Client:  &http.Client{Timeout: 10 * time.Second},
		maxKeep: 10 * time.Minute}
}

// Scrape fetches one sample; any failure is returned (telemetry loss).
func (o *Observer) Scrape(ctx context.Context) (Sample, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", o.MetricsURL, nil)
	if err != nil {
		return Sample{}, err
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return Sample{}, fmt.Errorf("scrape: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Sample{}, fmt.Errorf("scrape status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Sample{}, err
	}
	total, failed, found := parseCounters(string(raw), o.Slot)
	if !found {
		return Sample{}, fmt.Errorf("no lab_requests_total for slot %q", o.Slot)
	}
	s := Sample{At: time.Now(), Total: total, Failed: failed}
	o.mu.Lock()
	o.hist = append(o.hist, s)
	cut := s.At.Add(-o.maxKeep)
	i := 0
	for i < len(o.hist) && o.hist[i].At.Before(cut) {
		i++
	}
	o.hist = append([]Sample(nil), o.hist[i:]...)
	o.mu.Unlock()
	return s, nil
}

// WindowRatio returns the failure fraction over the trailing window using
// the newest sample and the oldest sample still inside the window.
// ok=false when history does not yet span the window.
func (o *Observer) WindowRatio(window time.Duration) (ratio float64, ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.hist) == 0 {
		return 0, false
	}
	end := o.hist[len(o.hist)-1]
	start := end
	for i := len(o.hist) - 1; i >= 0; i-- {
		if end.At.Sub(o.hist[i].At) <= window {
			start = o.hist[i]
		} else {
			break
		}
	}
	if end.At.Sub(start.At) < window-time.Second {
		return 0, false // window not yet spanned
	}
	dTotal := end.Total - start.Total
	if dTotal <= 0 {
		return 0, false
	}
	return (end.Failed - start.Failed) / dTotal, true
}

// AbortTripped reports whether the last n consecutive non-overlapping
// windows all exceeded maxRatio. Short history returns false (no decision
// without data — the runner treats prolonged indecision as failure).
func (o *Observer) AbortTripped(maxRatio float64, n int, window time.Duration) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.hist) == 0 {
		return false
	}
	end := o.hist[len(o.hist)-1].At
	for k := 0; k < n; k++ {
		wEnd := end.Add(-time.Duration(k) * window)
		wStart := wEnd.Add(-window)
		var s0, s1 *Sample
		for i := range o.hist {
			if !o.hist[i].At.After(wStart) && (s0 == nil || o.hist[i].At.After(s0.At)) {
				c := o.hist[i]
				s0 = &c
			}
			if !o.hist[i].At.After(wEnd) && (s1 == nil || o.hist[i].At.After(s1.At)) {
				c := o.hist[i]
				s1 = &c
			}
		}
		if s0 == nil || s1 == nil || s1.At.Sub(s0.At) < window-time.Second {
			return false
		}
		dTotal := s1.Total - s0.Total
		if dTotal <= 0 {
			return false
		}
		if (s1.Failed-s0.Failed)/dTotal <= maxRatio {
			return false
		}
	}
	return true
}

// parseCounters sums lab_requests_total for slot across results, splitting
// failed classes. Minimal exposition parser for our fixed label set.
func parseCounters(exp, slot string) (total, failed float64, found bool) {
	for _, line := range strings.Split(exp, "\n") {
		if !strings.HasPrefix(line, "lab_requests_total{") {
			continue
		}
		lb := strings.Index(line, "{")
		rb := strings.LastIndex(line, "}")
		if lb < 0 || rb < 0 {
			continue
		}
		labels := parseLabels(line[lb+1 : rb])
		if labels["slot"] != slot {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(line[rb+1:]), 64)
		if err != nil {
			continue
		}
		found = true
		total += val
		switch labels["result"] {
		case "server_error", "timeout", "transport_error":
			failed += val
		}
	}
	return total, failed, found
}

// parseLabels splits k="v",k="v" (no escapes in our label values).
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		parts := strings.SplitN(strings.TrimSpace(kv), "=", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = strings.Trim(parts[1], `"`)
	}
	return out
}
