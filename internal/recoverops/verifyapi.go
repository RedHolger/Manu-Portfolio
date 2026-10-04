package recoverops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"sre-portfolio/internal/telemetry"
)

func windowSelectors() (string, string, string) {
	base := `service="reservations",slot="stable"`
	return `lab_requests_total{` + base + `,result!="client_error"}`, `lab_requests_total{` + base + `,result="success"}`, `lab_request_duration_seconds_bucket{` + base + `,result="success",le="0.3"}`
}
func windowExprs() (string, string, string) {
	a, b, c := windowSelectors()
	return `sum(increase(` + a + `[10s]))`, `sum(increase(` + b + `[10s]))`, `sum(increase(` + c + `[10s]))`
}
func sumVector(ss []telemetry.Series) (float64, error) {
	if len(ss) == 0 {
		return 0, fmt.Errorf("missing series")
	}
	var total float64
	for _, s := range ss {
		v, err := telemetry.SampleValue(s.Value[1])
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, fmt.Errorf("invalid counter")
		}
		total += v
	}
	if math.IsNaN(total) || math.IsInf(total, 0) {
		return 0, fmt.Errorf("nonfinite sum")
	}
	return total, nil
}
func measureWindows(promURL string, actionAt, now time.Time) ([]VerifyWindow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return measureWindowsContext(ctx, promURL, actionAt, now)
}

// Verification uses only stable-slot telemetry. Presence, recent source sample
// timestamps and at least two scrapes per contributing series are checked before
// evaluating increases. Caller input never supplies counts or readiness claims.
func measureWindowsContext(ctx context.Context, promURL string, actionAt, now time.Time) ([]VerifyWindow, error) {
	now = now.Truncate(time.Second)
	if now.Add(-30 * time.Second).Before(actionAt) {
		return nil, fmt.Errorf("windows not fully post-action")
	}
	cl := telemetry.New(promURL)
	a, b, c := windowSelectors()
	selectors := []string{a, b, c}
	var out []VerifyWindow
	for i := 0; i < 3; i++ {
		end := now.Add(time.Duration(i-2) * 10 * time.Second)
		for _, edge := range []time.Time{end.Add(-10 * time.Second), end} {
			ss, e := cl.Query(ctx, b, edge)
			if e != nil {
				return nil, e
			}
			fs, e := cl.Query(ctx, c, edge)
			if e != nil {
				return nil, e
			}
			keys := func(xs []telemetry.Series) map[string]bool {
				out := map[string]bool{}
				for _, x := range xs {
					labels := map[string]string{}
					for k, v := range x.Metric {
						if k != "__name__" && k != "le" {
							labels[k] = v
						}
					}
					raw, _ := json.Marshal(labels)
					out[string(raw)] = true
				}
				return out
			}
			sk, fk := keys(ss), keys(fs)
			if len(sk) == 0 || len(sk) != len(fk) {
				return nil, fmt.Errorf("missing histogram series")
			}
			for k := range sk {
				if !fk[k] {
					return nil, fmt.Errorf("missing histogram bucket for success series")
				}
			}
		}
		var counts [3]float64
		for j, selector := range selectors {
			for _, edge := range []time.Time{end.Add(-10 * time.Second), end} {
				ss, err := cl.Query(ctx, `min(timestamp(`+selector+`))`, edge)
				if err != nil {
					return nil, err
				}
				stamp, err := sumVector(ss)
				if err != nil || float64(edge.Unix())-stamp > 5.1 || stamp > float64(edge.Unix()) {
					return nil, fmt.Errorf("window %d stale/missing source samples", i)
				}
			}
			ss, err := cl.Query(ctx, `min(count_over_time(`+selector+`[10s]))`, end)
			if err != nil {
				return nil, err
			}
			n, err := sumVector(ss)
			if err != nil || n < 2 {
				return nil, fmt.Errorf("window %d sparse telemetry", i)
			}
			ss, err = cl.Query(ctx, `sum(increase(`+selector+`[10s]))`, end)
			if err != nil {
				return nil, err
			}
			counts[j], err = sumVector(ss)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, VerifyWindow{Start: end.Add(-10 * time.Second), End: end, Eligible: counts[0], Success: counts[1], FastOK: counts[2], Estimated: true})
	}
	return out, nil
}
func lastExecution(s *Store, id string) (time.Time, string, error) {
	x, err := s.Intent(id)
	if err != nil {
		return time.Time{}, "", err
	}
	t, err := time.Parse(time.RFC3339Nano, x.Executed)
	return t, x.RV, err
}

// VerifyIncident is shared by the API and autonomous controller. It is bounded
// by the immutable execution timestamp and never performs another rollback.
func (s *Server) VerifyIncident(ctx context.Context, id string) (VerifyOutcome, error) {
	in, err := s.store.GetIncident(id)
	if err != nil {
		return VerifyOutcome{}, err
	}
	if in.State != StVerifying {
		return VerifyOutcome{}, fmt.Errorf("verification requires VERIFYING")
	}
	if s.patcher == nil {
		return VerifyOutcome{}, fmt.Errorf("live template check unavailable")
	}
	x, err := s.store.Intent(id)
	if errors.Is(err, sql.ErrNoRows) {
		_, e := s.store.Transition(id, StEscalated, `{"reason":"legacy verification lacks immutable execution intent"}`)
		return VerifyOutcome{Reason: "legacy verification requires operator reconciliation"}, e
	}
	if err != nil {
		return VerifyOutcome{}, err
	}
	acted, err := time.Parse(time.RFC3339Nano, x.Executed)
	if err != nil {
		return VerifyOutcome{}, err
	}
	now := time.Now().UTC()
	if now.Sub(acted) > 180*time.Second {
		_, err = s.store.Transition(id, StEscalated, `{"reason":"verification deadline exceeded"}`)
		return VerifyOutcome{Reason: "verification deadline exceeded"}, err
	}
	// Wait for all windows to be post-action; this is not missing telemetry.
	if now.Sub(acted) < 31*time.Second {
		return VerifyOutcome{Reason: "waiting for three post-action windows"}, nil
	}
	windows, err := measureWindowsContext(ctx, s.cfg.PrometheusURL, acted, now)
	if err != nil {
		return VerifyOutcome{Reason: err.Error()}, nil
	}
	live, _, err := s.patcher.Get(s.cfg.Policy.Deployment)
	if err != nil {
		return VerifyOutcome{}, err
	}
	vin := VerifyInput{DesiredPresent: live.UID == x.UID && live.TemplateHash == x.Desired,
		GenerationHit: live.Generation > 0 && live.ObservedGeneration >= live.Generation,
		ReadyReplicas: live.Ready, WantReplicas: live.Want, Windows: windows}
	out := VerifyRecovery(vin)
	if live.Updated != live.Want || live.Available < live.Want || live.Total != live.Want {
		out = VerifyOutcome{Reason: "rollout not fully available/updated"}
	}
	if !out.Recovered {
		return out, nil
	}
	// Do not commit a proof after the deadline due to slow telemetry calls.
	if time.Since(acted) > 180*time.Second {
		return VerifyOutcome{Reason: "verification deadline exceeded"}, nil
	}
	payload, err := json.Marshal(map[string]any{"schema_version": 2, "incident_id": id, "uid": x.UID, "desired_hash": x.Desired, "executed_at": x.Executed, "windows": windows, "rollout": live, "measured_at": now, "reason": out.Reason})
	if err != nil {
		return VerifyOutcome{}, err
	}
	if err = s.store.CompleteVerification(id, x.Executed, string(payload)); err != nil {
		return VerifyOutcome{}, err
	}
	return out, nil
}
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, 401, "unauthorized")
		return
	}
	id := r.PathValue("id")
	in, err := s.store.GetIncident(id)
	if err != nil {
		writeErr(w, 404, "no such incident")
		return
	}
	if in.State != StVerifying {
		writeErr(w, 409, "verification requires VERIFYING")
		return
	}
	if s.patcher == nil {
		writeErr(w, 503, "live template check unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	out, err := s.VerifyIncident(ctx, id)
	if err != nil {
		writeErr(w, 503, err.Error())
		return
	}
	code := 422
	if out.Recovered {
		code = 200
	}
	writeJSON(w, code, out)
}
