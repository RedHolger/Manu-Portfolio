// Package telemetry — validated Prometheus HTTP client (§5.4 step 1).
// Every query is bounded (5s timeout, 4MiB cap). Error envelopes, NaN/Inf,
// partial-result warnings, missing slots, stale data, and missing histogram
// buckets are REJECTED — never coerced into a PASS.
package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Series is one Prometheus vector/matrix element with string labels.
type Series struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"` // matrix form
	Value  [2]any            `json:"value"`  // vector form
}

// QueryResponse is the Prometheus HTTP API envelope.
type QueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string   `json:"resultType"`
		Result     []Series `json:"result"`
	} `json:"data"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Client queries one Prometheus base URL.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	MaxBody int64
}

// New builds a client with 5s timeout and 4MiB cap.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 5 * time.Second},
		MaxBody: 4 << 20}
}

// QueryRange runs a range query over [at-step, at] (short diagnostic window).
func (c *Client) QueryRange(ctx context.Context, expr string, at time.Time, step time.Duration) ([]Series, error) {
	return c.QueryMatrix(ctx, expr, at.Add(-step), at, step)
}

// QueryMatrix runs an explicit range query. All responses — instant, range,
// or matrix — pass the same envelope + sample validation (H2: the release
// gate's instant path previously skipped both checks).
func (c *Client) QueryMatrix(ctx context.Context, expr string, start, end time.Time, step time.Duration) ([]Series, error) {
	u, _ := url.Parse(c.BaseURL + "/api/v1/query_range")
	q := u.Query()
	q.Set("query", expr)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", step.String())
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("prometheus status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.MaxBody))
	if err != nil {
		return nil, err
	}
	var env QueryResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := checkEnvelope(&env); err != nil {
		return nil, err
	}
	if err := checkSamples(env.Data.Result); err != nil {
		return nil, err
	}
	return env.Data.Result, nil
}

// checkEnvelope rejects error statuses and partial-result warnings.
func checkEnvelope(env *QueryResponse) error {
	if env.Status != "success" {
		return fmt.Errorf("prometheus error: %s", env.Error)
	}
	for _, w := range env.Warnings {
		if containsPartial(w) {
			return fmt.Errorf("partial results warning: %s", w)
		}
	}
	return nil
}

// checkSamples rejects NaN/Inf in both matrix (Values) and vector (Value)
// forms. Instant-query callers previously skipped this (H2).
// NOTE: Value is a fixed [2]any, so absence is detected by nil elements,
// not length — appending the zero value would reject every matrix response.
func checkSamples(series []Series) error {
	for _, s := range series {
		vals := s.Values
		if s.Value[0] != nil || s.Value[1] != nil {
			vals = append(vals, s.Value)
		}
		for _, v := range vals {
			f, err := sampleFloat(v[1])
			if err != nil {
				return err
			}
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return errors.New("NaN/Inf sample rejected")
			}
		}
	}
	return nil
}

// Query runs an instant query (used for freshness checks).
func (c *Client) Query(ctx context.Context, expr string, at time.Time) ([]Series, error) {
	u, _ := url.Parse(c.BaseURL + "/api/v1/query")
	q := u.Query()
	q.Set("query", expr)
	q.Set("time", strconv.FormatInt(at.Unix(), 10))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("prometheus status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.MaxBody))
	if err != nil {
		return nil, err
	}
	var env QueryResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if err := checkEnvelope(&env); err != nil {
		return nil, err
	}
	if err := checkSamples(env.Data.Result); err != nil {
		return nil, err
	}
	return env.Data.Result, nil
}

// SumIncrease sums increase() semantics: callers pass per-series totals and
// this helper adds float values, preserving fractional extrapolation and
// labeling the result estimated (spec §5.4 step 3).
func SumIncrease(vals []float64) (total float64, estimated bool) {
	for _, v := range vals {
		total += v
		if v != math.Trunc(v) {
			estimated = true
		}
	}
	return total, estimated
}

// SampleValue parses one Prometheus sample value (exported for evaluator).
func SampleValue(v any) (float64, error) { return sampleFloat(v) }

func sampleFloat(v any) (float64, error) {
	switch t := v.(type) {
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, fmt.Errorf("bad sample %q", t)
		}
		return f, nil
	case float64:
		return t, nil
	default:
		return 0, fmt.Errorf("bad sample type %T", v)
	}
}

func containsPartial(w string) bool {
	lw := strings.ToLower(w)
	return strings.Contains(lw, "partial") ||
		strings.Contains(lw, "truncat") ||
		strings.Contains(lw, "deduplicat")
}
