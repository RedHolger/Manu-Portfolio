// Regression test for the _count{le=} bug: fast-good must come from the
// cumulative histogram BUCKET (count/sum carry no le label). The old query
// matched nothing on healthy traffic, yielding SlowBad=0 always and masking
// real slowness. Fake Prometheus dispatches per query shape.
package budgetguard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sre-portfolio/internal/telemetry"
)

func fakeQueryServer(t *testing.T) *httptest.Server {
	t.Helper()
	valFor := func(q string) string {
		val := "1100" // total INCLUDING 100 client_errors (must be excluded)
		switch {
		case strings.Contains(q, "timestamp("):
			val = "5" // fresh
		case strings.Contains(q, "result=~"):
			val = "10" // bad
		case strings.Contains(q, "_bucket"):
			val = "950" // fast-good within 0.3s
		case strings.Contains(q, `!="client_error"`):
			val = "1000" // eligible population (client errors excluded)
		case strings.Contains(q, `result="client_error"`):
			val = "100" // present but never counted toward any SLI
		}
		return val
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		val := valFor(q)
		if strings.Contains(r.URL.Path, "query_range") {
			// Full 300s coverage: 21 points, step 15s, ending now.
			now := time.Now().UTC().Unix()
			var pts []string
			for i := 20; i >= 0; i-- {
				pts = append(pts, fmt.Sprintf("[%d,\"%s\"]", now-int64(i*15), val))
			}
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"matrix","result":[` +
					`{"metric":{},"values":[` + strings.Join(pts, ",") + `]}]}}`))
			return
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"vector","result":[` +
				`{"metric":{},"value":[1790537357,"` + val + `"]}]}}`))
	}))
}

func TestFetchCountsUsesBucketForFast(t *testing.T) {
	s := fakeQueryServer(t)
	defer s.Close()
	cfg := mustConfig(t)
	end := time.Now().UTC()
	f, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, end)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	for name, sc := range map[string]SlotCounts{"stable": f.DisplayStable, "candidate": f.DisplayCandidate} {
		if sc.Eligible != 1000 || sc.Bad != 10 || sc.SlowBad != 50 {
			t.Fatalf("%s: got %+v, want eligible=1000 bad=10 slow_or_bad=50", name, sc)
		}
	}
}

// TestClientErrorExcluded proves the eligible population subtracts the 100
// client_errors the fake serves: the evaluator must query result!="client_error".
// Pre-exclusion code read the 1100 total and fails this test.
func TestClientErrorExcluded(t *testing.T) {
	s := fakeQueryServer(t)
	defer s.Close()
	cfg := mustConfig(t)
	f, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, time.Now().UTC())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if f.DisplayStable.Eligible != 1000 {
		t.Fatalf("eligible=%d, want 1000 (100 client_errors excluded)", f.DisplayStable.Eligible)
	}
}

// TestNoPrematureRounding (H6): fractional increase values gate unrounded.
// eligible=2000, bad=20.4999 → 1.0249% > 1% must FAIL; rounding first would
// give exactly 1.0% → PASS (boundary equality). Estimated must be set.
func TestNoPrematureRounding(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		val := "2000"
		switch {
		case strings.Contains(r.URL.Path, "query_range"):
			now := time.Now().UTC().Unix()
			var pts []string
			for i := 20; i >= 0; i-- {
				pts = append(pts, fmt.Sprintf("[%d,\"2000\"]", now-int64(i*15)))
			}
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"matrix","result":[` +
					`{"metric":{},"values":[` + strings.Join(pts, ",") + `]}]}}`))
			return
		case strings.Contains(q, "timestamp("):
			val = "5"
		case strings.Contains(q, "result=~"):
			// Healthy stable baseline; boundary-straddling candidate.
			val = "10"
			if strings.Contains(q, `slot="candidate"`) {
				val = "20.4999"
			}
		case strings.Contains(q, "_bucket"):
			val = "1990"
			if strings.Contains(q, `slot="candidate"`) {
				val = "1979.5001"
			}
		case strings.Contains(q, `!="client_error"`):
			val = "2000"
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"vector","result":[` +
				`{"metric":{},"value":[1790537357,"` + val + `"]}]}}`))
	}))
	defer s.Close()
	cfg := mustConfig(t)
	end := time.Now().UTC()
	f, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, end)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	res := DecideCounts(cfg, end, f.Stable, f.Candidate,
		f.DisplayStable, f.DisplayCandidate, f.Estimated)
	if res.Decision != "FAIL" {
		t.Fatalf("got %s %v — fractional bad count was rounded before gating", res.Decision, res.Reasons)
	}
	if !res.Estimated {
		t.Fatal("fractional inputs must set estimated_counts")
	}
}

// TestThresholdBucketQueried (H6): the fast-good query must use the
// configured threshold, not a hard-coded le="0.3".
func TestThresholdBucketQueried(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		mu.Lock()
		seen = append(seen, q)
		mu.Unlock()
		if strings.Contains(r.URL.Path, "query_range") {
			now := time.Now().UTC().Unix()
			var pts []string
			for i := 20; i >= 0; i-- {
				pts = append(pts, fmt.Sprintf("[%d,\"100\"]", now-int64(i*15)))
			}
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"matrix","result":[` +
					`{"metric":{},"values":[` + strings.Join(pts, ",") + `]}]}}`))
			return
		}
		if strings.Contains(q, "timestamp(") {
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"vector","result":[` +
					`{"metric":{},"value":[1790537357,"5"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"vector","result":[` +
				`{"metric":{},"value":[1790537357,"100"]}]}}`))
	}))
	defer s.Close()
	cfg := mustConfig(t)
	cfg.LatencyThreshold = 0.5
	_, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, time.Now().UTC())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	found := false
	for _, q := range seen {
		if strings.Contains(q, "_bucket") {
			found = true
			if !strings.Contains(q, `le="0.5"`) {
				t.Fatalf("bucket query uses wrong threshold: %s", q)
			}
		}
	}
	if !found {
		t.Fatal("no bucket query issued")
	}
}

// TestMissingBucketUnknown proves an absent required histogram bucket yields
// an error (→ INCONCLUSIVE), never a fabricated zero. D2: the freshness
// query must answer fresh here, or the test exits before reaching the bucket.
func TestMissingBucketUnknown(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if strings.Contains(r.URL.Path, "query_range") {
			// Full coverage so the test reaches the bucket query.
			now := time.Now().UTC().Unix()
			var pts []string
			for i := 20; i >= 0; i-- {
				pts = append(pts, fmt.Sprintf("[%d,\"100\"]", now-int64(i*15)))
			}
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"matrix","result":[` +
					`{"metric":{},"values":[` + strings.Join(pts, ",") + `]}]}}`))
			return
		}
		if strings.Contains(q, "timestamp(") {
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"vector","result":[` +
					`{"metric":{},"value":[1790537357,"5"]}]}}`))
			return
		}
		if strings.Contains(q, "_bucket") {
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"vector","result":[` +
				`{"metric":{},"value":[1790537357,"1000"]}]}}`))
	}))
	defer s.Close()
	cfg := mustConfig(t)
	_, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, time.Now().UTC())
	if err == nil {
		t.Fatal("missing bucket must error (INCONCLUSIVE), not fabricate")
	}
	if !strings.Contains(err.Error(), "fast") {
		t.Fatalf("expected the bucket query to fail, got: %v", err)
	}
}
