// webhook.go — Alertmanager webhook ingestion and incident HTTP API (R1).
// Every accepted occurrence is committed to SQLite before the 202 reply.
// Duplicate deliveries dedupe on the delivery identity; out-of-order or
// unknown resolved messages never open or reopen a case. R1 performs no
// cluster mutation: there is no Kubernetes client in this package.
package recoverops

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Occurrence is one validated Alertmanager delivery (subset contract).
type Occurrence struct {
	Status       string            `json:"status"`
	Fingerprint  string            `json:"fingerprint"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Labels       map[string]string `json:"labels"`
}

// Disposition reports what one occurrence did.
type Disposition struct {
	Fingerprint string `json:"fingerprint"`
	Occurrence  string `json:"occurrence_id"`
	Incident    string `json:"incident_id,omitempty"`
	Result      string `json:"result"`
}

// Terminal reports whether no further alert transitions apply.
func Terminal(state string) bool {
	return state == StResolved || state == StCancelled
}

// parseOccurrence validates one raw occurrence against the policy pins.
// Alerts that select any other namespace/deployment/labels are rejected:
// alerts must not select arbitrary resources.
func parseOccurrence(raw json.RawMessage, pol Policy) (Occurrence, error) {
	var o Occurrence
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return o, fmt.Errorf("bad occurrence: %w", err)
	}
	if o.Status != "firing" && o.Status != "resolved" {
		return o, fmt.Errorf("status %q must be firing|resolved", o.Status)
	}
	if o.Fingerprint == "" {
		return o, fmt.Errorf("fingerprint required")
	}
	if _, err := time.Parse(time.RFC3339, o.StartsAt); err != nil {
		return o, fmt.Errorf("bad startsAt: %w", err)
	}
	if o.Status == "resolved" {
		if o.EndsAt == "" {
			return o, fmt.Errorf("resolved occurrence requires endsAt")
		}
		if _, err := time.Parse(time.RFC3339, o.EndsAt); err != nil {
			return o, fmt.Errorf("bad endsAt: %w", err)
		}
	} else if o.EndsAt != "" {
		if _, err := time.Parse(time.RFC3339, o.EndsAt); err != nil {
			return o, fmt.Errorf("bad endsAt: %w", err)
		}
	}
	if o.Labels["namespace"] != pol.Namespace || o.Labels["deployment"] != pol.Deployment {
		return o, fmt.Errorf("target %s/%s outside pinned %s/%s",
			o.Labels["namespace"], o.Labels["deployment"], pol.Namespace, pol.Deployment)
	}
	for k, want := range pol.MatchLabels {
		if o.Labels[k] != want {
			return o, fmt.Errorf("label %s=%q does not match policy %q", k, o.Labels[k], want)
		}
	}
	return o, nil
}

// occurrenceID binds policy revision, fingerprint and startsAt: the firing
// and its later resolved message share it, so pairs reconcile.
func occurrenceID(policyHash string, o Occurrence) string {
	sum := sha256.Sum256([]byte(policyHash + "\x00" + o.Fingerprint + "\x00" + o.StartsAt))
	return fmt.Sprintf("%x", sum)
}

// deliveryID identifies one exact delivery for redelivery dedupe.
func deliveryID(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum)
}

// Server serves the R1 HTTP surface.
type Server struct {
	cfg   Config
	store *Store
	mux   *http.ServeMux
	// mu serializes occurrence processing: two concurrent identical
	// firings must not race past the duplicate check (R1 throughput is
	// webhook-scale; revisit only with measured contention).
	mu sync.Mutex
}

// NewServer wires routes. The store must already be open.
func NewServer(cfg Config, store *Store) *Server {
	s := &Server{cfg: cfg, store: store, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/alerts", s.handleAlerts)
	s.mux.HandleFunc("GET /v1/incidents", s.handleList)
	s.mux.HandleFunc("GET /v1/incidents/{id}", s.handleShow)
	s.mux.HandleFunc("POST /v1/incidents/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	return s
}

// Handler exposes the mux (tests serve it with httptest).
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) authed(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	want := "Bearer " + s.cfg.Token
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// handleAlerts validates the whole batch first (any invalid occurrence
// rejects the batch with 400 and commits nothing), then commits each
// occurrence before replying 202 with per-occurrence dispositions.
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "too large") {
			writeErr(w, http.StatusRequestEntityTooLarge, "body exceeds 1MiB")
			return
		}
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	var batch []json.RawMessage
	if err := json.Unmarshal(raw, &batch); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be a JSON array of occurrences")
		return
	}
	if len(batch) == 0 {
		writeErr(w, http.StatusBadRequest, "empty batch")
		return
	}
	occs := make([]Occurrence, 0, len(batch))
	for _, rb := range batch {
		o, err := parseOccurrence(rb, s.cfg.Policy)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		occs = append(occs, o)
	}
	out, err := s.ingest(batch, occs)
	if err != nil {
		// Persistence failed: 503 so Alertmanager retries the batch.
		writeErr(w, http.StatusServiceUnavailable, "persistence unavailable")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"results": out})
}

// IngestBatch validates and commits a saved delivery batch without HTTP
// (replay CLI and tests). Validation is all-or-nothing like the webhook.
func IngestBatch(st *Store, pol Policy, batch []json.RawMessage) ([]Disposition, error) {
	occs := make([]Occurrence, 0, len(batch))
	for _, rb := range batch {
		o, err := parseOccurrence(rb, pol)
		if err != nil {
			return nil, err
		}
		occs = append(occs, o)
	}
	s := &Server{cfg: Config{Policy: pol, MaxBody: DefaultMaxBody}, store: st}
	return s.ingest(batch, occs)
}

// ingest commits validated occurrences in order (shared by the webhook
// and the replay CLI). Any persistence error aborts with the batch prefix
// already committed — dispositions for completed occurrences are lost to
// the caller, but the rows are durable and redelivery dedupes.
func (s *Server) ingest(batch []json.RawMessage, occs []Occurrence) ([]Disposition, error) {
	var out []Disposition
	for i, o := range occs {
		d, err := s.process(o, batch[i])
		if err != nil {
			return out, err
		}
		out = append(out, d)
	}
	return out, nil
}

// process commits one validated occurrence and reports its disposition.
func (s *Server) process(o Occurrence, raw json.RawMessage) (Disposition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	occID := occurrenceID(s.cfg.Policy.Hash, o)
	delID := deliveryID(raw)
	d := Disposition{Fingerprint: o.Fingerprint, Occurrence: occID}
	known, err := s.store.IncidentForOccurrence(occID)
	knownExists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	firing := o.Status == "firing"
	if !knownExists {
		if !firing {
			// Unknown resolved: record the delivery (audit) without opening
			// any case; it must not trigger remediation.
			if _, err := s.store.RecordDelivery(delID, occID, "", false, o.StartsAt, o.EndsAt); err != nil {
				return d, err
			}
			d.Result = "ignored-unknown-resolved"
			return d, nil
		}
		ev, _ := json.Marshal(map[string]interface{}{"labels": o.Labels, "startsAt": o.StartsAt})
		in, _, err := s.store.CreateIncident(occID, "", s.cfg.Policy.Hash, string(ev))
		if err != nil {
			return d, err
		}
		if _, err := s.store.RecordDelivery(delID, occID, in.ID, true, o.StartsAt, o.EndsAt); err != nil {
			return d, err
		}
		d.Incident, d.Result = in.ID, "created"
		return d, nil
	}
	d.Incident = known.ID
	if Terminal(known.State) {
		// Terminal occurrences never reopen, whether the late message is
		// firing or resolved. The delivery is still recorded for audit.
		if _, err := s.store.RecordDelivery(delID, occID, known.ID, firing, o.StartsAt, o.EndsAt); err != nil {
			return d, err
		}
		d.Result = "ignored-terminal"
		return d, nil
	}
	if !firing {
		if _, err := s.store.RecordDelivery(delID, occID, known.ID, false, o.StartsAt, o.EndsAt); err != nil {
			return d, err
		}
		if _, err := s.store.Transition(known.ID, StResolved, `{"by":"resolved-alert"}`); err != nil {
			return d, err
		}
		d.Result = "resolved"
		return d, nil
	}
	dup, err := s.store.RecordDelivery(delID, occID, known.ID, true, o.StartsAt, o.EndsAt)
	if err != nil {
		return d, err
	}
	if dup {
		d.Result = "duplicate"
		return d, nil
	}
	d.Result = "ok"
	return d, nil
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit, offset := int64(20), int64(0)
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "bad limit")
			return
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "bad offset")
			return
		}
		offset = n
	}
	list, err := s.store.ListIncidents(limit, offset)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "persistence unavailable")
		return
	}
	if list == nil {
		list = []Incident{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"incidents": list})
}

func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	in, err := s.store.GetIncident(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such incident")
		return
	}
	evs, err := s.store.Events(id)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "persistence unavailable")
		return
	}
	if evs == nil {
		evs = []Event{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"incident": in, "events": evs})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	in, err := s.store.Transition(id, StCancelled, `{"by":"cancel-api"}`)
	if err != nil {
		if strings.Contains(err.Error(), "illegal transition") {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusNotFound, "no such incident")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"incident": in})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "store unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ready": "true"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.CountByState()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "store unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "# HELP recoverops_up controller process up")
	_, _ = fmt.Fprintln(w, "# TYPE recoverops_up gauge")
	_, _ = fmt.Fprintln(w, "recoverops_up 1")
	_, _ = fmt.Fprintln(w, "# HELP recoverops_incidents incidents by state")
	_, _ = fmt.Fprintln(w, "# TYPE recoverops_incidents gauge")
	for _, st := range []string{StReceived, StResolved, StCancelled} {
		_, _ = fmt.Fprintf(w, "recoverops_incidents{state=%q} %d\n", st, counts[st])
	}
}
