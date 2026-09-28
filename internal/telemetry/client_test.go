// Contract tests: fake Prometheus HTTP server (no Docker needed).
package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fakeServer(body string, status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestRejectsErrorEnvelope(t *testing.T) {
	s := fakeServer(`{"status":"error","error":"bad query"}`, 200)
	defer s.Close()
	if _, err := New(s.URL).Query(context.Background(), "up", time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRejectsHTTP500(t *testing.T) {
	s := fakeServer(`oops`, 500)
	defer s.Close()
	if _, err := New(s.URL).Query(context.Background(), "up", time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRejectsPartialWarning(t *testing.T) {
	s := fakeServer(`{"status":"success","data":{"resultType":"vector","result":[]},"warnings":["partial result: remote storage"]} `, 200)
	defer s.Close()
	if _, err := New(s.URL).QueryRange(context.Background(), "up", time.Now(), time.Minute); err == nil {
		t.Fatal("expected partial-warning rejection")
	}
}

func TestRejectsNaN(t *testing.T) {
	s := fakeServer(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"NaN"]]}]}}`, 200)
	defer s.Close()
	if _, err := New(s.URL).QueryRange(context.Background(), "up", time.Now(), time.Minute); err == nil {
		t.Fatal("expected NaN rejection")
	}
}

func TestAcceptsVector(t *testing.T) {
	s := fakeServer(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"slot":"stable"},"value":[1,"42"]}]}}`, 200)
	defer s.Close()
	series, err := New(s.URL).Query(context.Background(), "up", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 {
		t.Fatalf("got %d series", len(series))
	}
}

func TestSumIncreaseFlagsFractional(t *testing.T) {
	total, est := SumIncrease([]float64{100.5, 200})
	if total != 300.5 || !est {
		t.Fatalf("got %v %v", total, est)
	}
	if _, est := SumIncrease([]float64{100, 200}); est {
		t.Fatal("integer sums must not be flagged estimated")
	}
}

// H2 regression: the instant Query path (used by the release gate) must
// apply the same warning + NaN/Inf validation as the range path.
func TestInstantQueryRejectsPartialWarning(t *testing.T) {
	s := fakeServer(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"7"]}]},"warnings":["partial result: thanos"]} `, 200)
	defer s.Close()
	if _, err := New(s.URL).Query(context.Background(), "up", time.Now()); err == nil {
		t.Fatal("expected partial-warning rejection on instant path")
	}
}

func TestInstantQueryRejectsNaN(t *testing.T) {
	s := fakeServer(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"NaN"]}]}}`, 200)
	defer s.Close()
	if _, err := New(s.URL).Query(context.Background(), "up", time.Now()); err == nil {
		t.Fatal("expected NaN rejection on instant path")
	}
}

func TestInstantQueryRejectsInf(t *testing.T) {
	s := fakeServer(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"+Inf"]}]}}`, 200)
	defer s.Close()
	if _, err := New(s.URL).Query(context.Background(), "up", time.Now()); err == nil {
		t.Fatal("expected +Inf rejection on instant path")
	}
}
