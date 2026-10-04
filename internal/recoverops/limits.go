package recoverops

import (
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Limits reports the same durable reservations used by prepareIntent. Reserving
// an ambiguous attempt consumes budget conservatively even when later escalated.
func (s *Store) Limits(pol Policy, now time.Time) (map[string]any, error) {
	if pol.PerHour < 1 {
		return nil, fmt.Errorf("invalid hourly limit")
	}
	rows, err := s.db.Query(budgetReservationsSQL, TargetUID(pol), TargetUID(pol))
	if err != nil {
		return nil, err
	}
	var starts []time.Time
	var last time.Time
	for rows.Next() {
		var ts string
		if err = rows.Scan(&ts); err != nil {
			rows.Close()
			return nil, err
		}
		t, e := time.Parse(time.RFC3339Nano, ts)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if t.After(last) {
			last = t
		}
		if !t.Before(now.Add(-time.Hour)) {
			starts = append(starts, t)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	next := now
	if t := last.Add(time.Duration(pol.CooldownSecs) * time.Second); t.After(next) {
		next = t
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	if int64(len(starts)) >= pol.PerHour {
		t := starts[len(starts)-int(pol.PerHour)].Add(time.Hour + time.Second)
		if t.After(next) {
			next = t
		}
	}
	var active int
	err = s.db.QueryRow(`SELECT count(*) FROM execution_intents x JOIN incidents i ON i.id=x.incident_id WHERE x.target=? AND i.state IN ('EXECUTING','RECONCILING','VERIFYING')`, TargetUID(pol)).Scan(&active)
	return map[string]any{"used_last_hour": len(starts), "per_hour": pol.PerHour, "next_eligible_at": next.UTC(), "active": active, "policy_hash": pol.Hash}, err
}
func (s *Server) handleLimits(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, 401, "unauthorized")
		return
	}
	out, err := s.store.Limits(s.cfg.Policy, time.Now())
	if err != nil {
		writeErr(w, 503, "persistence unavailable")
		return
	}
	writeJSON(w, 200, out)
}
