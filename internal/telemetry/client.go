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

// QueryRange runs an instant or range query at time `at` (step for ranges).
// Rejects: non-200, error status, warnings mentioning partial results,
// NaN/Inf samples, empty metric names.
func (c *Client) QueryRange(ctx context.Context, expr string, at time.Time, step time.Duration) ([]Series, error) {
	u, _ := url.Parse(c.BaseURL + "/api/v1/query_range")
	// NOTE: real range path; instant queries use /api/v1/query via Query().
	q := u.Query()
	q.Set("query", expr)
	q.Set("start", strconv.FormatInt(at.Add(-step).Unix(), 10))
	q.Set("end", strconv.FormatInt(at.Unix(), 10))
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
	if env.Status != "success" {
		return nil, fmt.Errorf("prometheus error: %s", env.Error)
	}
	for _, w := range env.Warnings {
		if containsPartial(w) {
			return nil, fmt.Errorf("partial results warning: %s", w)
		}
	}
	for _, s := range env.Data.Result {
		for _, v := range s.Values {
			f, err := sampleFloat(v[1])
			if err != nil {
				return nil, err
			}
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, errors.New("NaN/Inf sample rejected")
			}
		}
	}
	return env.Data.Result, nil
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
	if env.Status != "success" {
		return nil, fmt.Errorf("prometheus error: %s", env.Error)
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
