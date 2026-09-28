// gateway.go — F2: real admin-API injector for the lab gateway.
// Identity is enforced at construction (expected context recorded for
// audit; the runner verifies the live context separately via kubectl).
// Every mutation is bounded (client timeout) and idempotent by gateway
// contract; intent is journaled by the caller BEFORE Apply.
package faultlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GatewayInjector talks to the lab gateway admin port.
type GatewayInjector struct {
	BaseURL string // e.g. http://127.0.0.1:8082
	Token   string
	Client  *http.Client
	Context string // expected kube context (audit label)
}

// NewGatewayInjector builds an injector with a 10s client bound.
func NewGatewayInjector(baseURL, token, context string) *GatewayInjector {
	return &GatewayInjector{BaseURL: baseURL, Token: token, Context: context,
		Client: &http.Client{Timeout: 10 * time.Second}}
}

func (g *GatewayInjector) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.BaseURL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("gateway admin: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, nil
}

// Apply installs one bounded fault (idempotent by ID).
func (g *GatewayInjector) Apply(ctx context.Context, in Injection) error {
	var kind string
	switch in.Kind {
	case FaultDelay:
		kind = "gateway_delay"
	case FaultConnFail:
		kind = "conn_fail"
	case FaultDepOutage:
		kind = "dependency_failure"
	default:
		return fmt.Errorf("unsupported fault kind %q", in.Kind)
	}
	code, raw, err := g.do(ctx, "PUT", "/admin/faults/"+in.ID, map[string]any{
		"kind": kind, "slot": in.Slot,
		"delayMilliseconds": in.DelayMs, "fraction": in.Fraction,
		"ttlSeconds": in.TTL,
	})
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("apply %s: status %d: %s", in.ID, code, string(raw))
	}
	return nil
}

// Clear removes one fault (idempotent: unknown IDs succeed).
func (g *GatewayInjector) Clear(ctx context.Context, id string) error {
	code, raw, err := g.do(ctx, "DELETE", "/admin/faults/"+id, nil)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("clear %s: status %d: %s", id, code, string(raw))
	}
	return nil
}

// ClearAll is the emergency path (idempotent).
func (g *GatewayInjector) ClearAll(ctx context.Context) error {
	code, raw, err := g.do(ctx, "DELETE", "/admin/faults", nil)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("clear-all: status %d: %s", code, string(raw))
	}
	return nil
}

// Active lists live fault IDs; empty (not error) means clean.
func (g *GatewayInjector) Active(ctx context.Context) ([]string, error) {
	code, raw, err := g.do(ctx, "GET", "/admin/state", nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("state: status %d", code)
	}
	var st struct {
		Faults []map[string]any `json:"faults"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	var out []string
	for _, f := range st.Faults {
		if id, ok := f["ID"].(string); ok {
			out = append(out, id)
		}
	}
	return out, nil
}
