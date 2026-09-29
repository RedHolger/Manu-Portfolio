// depfault.go — dependency-fault control boundary (runner integration).
// The lab API exposes a token-gated, localhost-only admin endpoint with
// monotonic TTL expiry independent of any runner.
package faultlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// DepFaultCtl arms, clears, and queries the API dependency fault.
type DepFaultCtl interface {
	SetFail(ctx context.Context, ttlSecs int64) error
	Clear(ctx context.Context) error
	IsFailing(ctx context.Context) (bool, error)
}

// HTTPDepFault drives the lab API admin endpoint.
type HTTPDepFault struct {
	BaseURL string // e.g. http://127.0.0.1:8084 (port-forwarded)
	Token   string
	Client  *http.Client
}

// NewHTTPDepFault builds a controller with a 10s bound.
func NewHTTPDepFault(baseURL, token string) *HTTPDepFault {
	return &HTTPDepFault{BaseURL: baseURL, Token: token,
		Client: &http.Client{Timeout: 10 * time.Second}}
}

func (h *HTTPDepFault) put(ctx context.Context, fail bool, ttl int64) error {
	raw, _ := json.Marshal(map[string]any{"fail": fail, "ttlSeconds": ttl})
	req, err := http.NewRequestWithContext(ctx, "PUT", h.BaseURL+"/admin/depfault", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("depfault: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != 200 {
		return fmt.Errorf("depfault status %d", resp.StatusCode)
	}
	return nil
}

// SetFail arms the fault for ttlSecs.
func (h *HTTPDepFault) SetFail(ctx context.Context, ttlSecs int64) error {
	return h.put(ctx, true, ttlSecs)
}

// Clear disarms (idempotent).
func (h *HTTPDepFault) Clear(ctx context.Context) error {
	return h.put(ctx, false, 0)
}

// IsFailing queries current state.
func (h *HTTPDepFault) IsFailing(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", h.BaseURL+"/admin/depfault", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	resp, err := h.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("depfault: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("depfault status %d", resp.StatusCode)
	}
	var b struct {
		Failing bool `json:"failing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return false, err
	}
	return b.Failing, nil
}

// FakeDepFault is a scripted in-memory controller for tests.
type FakeDepFault struct {
	mu         sync.Mutex
	failing    bool
	expires    time.Time
	SetCalls   int
	ClearCalls int
	Err        error // injected failure for all ops
}

// NewFakeDepFault builds a disarmed fake.
func NewFakeDepFault() *FakeDepFault { return &FakeDepFault{} }

func (f *FakeDepFault) SetFail(_ context.Context, ttlSecs int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.SetCalls++
	f.failing = true
	f.expires = time.Now().Add(time.Duration(ttlSecs) * time.Second)
	return nil
}

func (f *FakeDepFault) Clear(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.ClearCalls++
	f.failing = false
	return nil
}

// IsFailing applies lazy TTL expiry like the lab API.
func (f *FakeDepFault) IsFailing(_ context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return false, f.Err
	}
	if f.failing && time.Now().After(f.expires) {
		f.failing = false
	}
	return f.failing, nil
}
