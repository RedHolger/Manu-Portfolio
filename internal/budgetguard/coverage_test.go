// coverage_test.go — H1: short windows must not pass as full windows.
package budgetguard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sre-portfolio/internal/telemetry"
)

// matrixServer serves a synthetic counter matrix: points every 15s over
// [start,end] with the given Unix-second holes removed.
func matrixServer(t *testing.T, start time.Time, holes map[int64]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var vals []string
		for ts := start.Unix(); ts <= start.Add(300*time.Second).Unix(); ts += 15 {
			if holes[ts] {
				continue
			}
			vals = append(vals, fmt.Sprintf("[%d,\"%d\"]", ts, ts))
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"matrix","result":[` +
				`{"metric":{},"values":[` + strings.Join(vals, ",") + `]}]}}`))
	}))
}

func TestCoverageFullWindowPasses(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	s := matrixServer(t, start, nil)
	defer s.Close()
	cfg := mustConfig(t)
	if err := CheckCoverage(context.Background(), telemetry.New(s.URL),
		cfg, "stable", start, start.Add(300*time.Second)); err != nil {
		t.Fatalf("full window: %v", err)
	}
}

func TestCoverageShortWindowFails(t *testing.T) {
	// Only 80s of traffic in a 300s window: last sample ~220s before end.
	start := time.Now().UTC().Truncate(time.Second)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var vals []string
		end := start.Add(300 * time.Second).Unix()
		for ts := start.Unix(); ts <= end; ts += 15 {
			if float64(end-ts) > 90 { // traffic covers only the last ~80s
				continue
			}
			vals = append(vals, fmt.Sprintf("[%d,\"%d\"]", ts, ts))
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"matrix","result":[` +
				`{"metric":{},"values":[` + strings.Join(vals, ",") + `]}]}}`))
	}))
	defer s.Close()
	cfg := mustConfig(t)
	err := CheckCoverage(context.Background(), telemetry.New(s.URL),
		cfg, "stable", start, start.Add(300*time.Second))
	if err == nil {
		t.Fatal("80s of traffic in a 300s window must fail coverage")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestCoverageGapFails(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	holes := map[int64]bool{}
	// Grid-aligned holes (multiples of the 15s step): 90..165 → 105s gap.
	for ts := start.Add(90 * time.Second).Unix(); ts <= start.Add(165*time.Second).Unix(); ts += 15 {
		holes[ts] = true
	}
	s := matrixServer(t, start, holes)
	defer s.Close()
	cfg := mustConfig(t)
	if err := CheckCoverage(context.Background(), telemetry.New(s.URL),
		cfg, "stable", start, start.Add(300*time.Second)); err == nil {
		t.Fatal("75s gap must fail coverage")
	}
}

func TestCoverageEmptyFails(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer s.Close()
	cfg := mustConfig(t)
	start := time.Now().UTC()
	if err := CheckCoverage(context.Background(), telemetry.New(s.URL),
		cfg, "stable", start, start.Add(300*time.Second)); err == nil {
		t.Fatal("empty window must fail coverage")
	}
}

// B regression: real source samples spaced 60s apart must NOT pass. The
// old gate read query_range evaluation points, and Prometheus replays the
// last value across a scrape gap, so 60s spacing looked like a full 15s
// grid. count_over_time sees the raw samples: 5 of 20 buckets covered.
func TestCoverageSixtySecondScrapeSpacingFails(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var vals []string
		// Raw samples every 60s, on the 15s evaluation grid.
		for ts := start.Add(60 * time.Second).Unix(); ts <= start.Add(300*time.Second).Unix(); ts += 60 {
			vals = append(vals, fmt.Sprintf("[%d,\"3\"]", ts))
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"matrix","result":[` +
				`{"metric":{},"values":[` + strings.Join(vals, ",") + `]}]}}`))
	}))
	defer s.Close()
	cfg := mustConfig(t)
	err := CheckCoverage(context.Background(), telemetry.New(s.URL),
		cfg, "stable", start, start.Add(300*time.Second))
	if err == nil {
		t.Fatal("60s scrape spacing in a 300s window must fail coverage")
	}
	t.Logf("correctly rejected: %v", err)
}

// B regression: the same reproduction through FetchCounts — the caller maps
// this error to INCONCLUSIVE (exit 3), never PASS.
func TestFetchCountsSparseTelemetryInconclusive(t *testing.T) {
	start := time.Now().UTC()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if strings.Contains(r.URL.Path, "query_range") {
			var vals []string
			for ts := start.Add(60 * time.Second).Unix(); ts <= start.Add(300*time.Second).Unix(); ts += 60 {
				vals = append(vals, fmt.Sprintf("[%d,\"3\"]", ts))
			}
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"matrix","result":[` +
					`{"metric":{},"values":[` + strings.Join(vals, ",") + `]}]}}`))
			return
		}
		if strings.Contains(q, "timestamp(") {
			_, _ = w.Write([]byte(
				`{"status":"success","data":{"resultType":"vector","result":[` +
					`{"metric":{"service":"reservations"},"value":[` +
					fmt.Sprintf("%d", start.Add(300*time.Second).Unix()) + `,"5"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(
			`{"status":"success","data":{"resultType":"vector","result":[` +
				`{"metric":{},"value":[` + fmt.Sprintf("%d", start.Unix()) + `,"100"]}]}}`))
	}))
	defer s.Close()
	cfg := mustConfig(t)
	_, err := FetchCounts(context.Background(), telemetry.New(s.URL), cfg, start.Add(300*time.Second))
	if err == nil {
		t.Fatal("sparse telemetry must not yield counts (INCONCLUSIVE)")
	}
	if !strings.Contains(err.Error(), "coverage") {
		t.Fatalf("expected a coverage error, got: %v", err)
	}
	t.Logf("correctly inconclusive: %v", err)
}
