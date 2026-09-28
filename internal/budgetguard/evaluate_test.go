// Regression test for the _count{le=} bug: fast-good must come from the
// cumulative histogram BUCKET (count/sum carry no le label). The old query
// matched nothing on healthy traffic, yielding SlowBad=0 always and masking
// real slowness. Fake Prometheus dispatches per query shape.
package budgetguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sre-portfolio/internal/telemetry"
)

func fakeQueryServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
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
	stable, cand, _, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, end)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	for name, sc := range map[string]SlotCounts{"stable": stable, "candidate": cand} {
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
	stable, _, _, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, time.Now().UTC())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if stable.Eligible != 1000 {
		t.Fatalf("eligible=%d, want 1000 (100 client_errors excluded)", stable.Eligible)
	}
}

// TestMissingBucketUnknown proves an absent required histogram bucket yields
// an error (→ INCONCLUSIVE), never a fabricated zero.
func TestMissingBucketUnknown(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
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
	_, _, _, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, time.Now().UTC())
	if err == nil {
		t.Fatal("missing bucket must error (INCONCLUSIVE), not fabricate")
	}
}
