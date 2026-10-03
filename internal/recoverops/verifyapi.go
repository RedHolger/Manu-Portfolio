// verifyapi.go — R4 verification persistence (live endpoint + helper).
// RESOLVED requires a persisted rollout + telemetry verification record,
// never a bare Alertmanager resolved notification: POST
// /v1/incidents/{id}/verify accepts a VerifyInput, re-checks the live
// template against the known-good snapshot, runs VerifyRecovery, and on
// pass journals a `verified` event and transitions VERIFYING → RESOLVED.
// Anything else leaves the incident open (422) so the matrix driver must
// present real passing windows. Kept in this file (not webhook.go) so the
// no-k8s ingest-path test stays narrow: only k8s.go, execute.go and this
// file may import client-go types.
package recoverops

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// handleVerify persists a verification record for a VERIFYING incident.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
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
	if in.State != StVerifying {
		writeErr(w, http.StatusConflict,
			fmt.Sprintf("verification requires VERIFYING, incident is %s", in.State))
		return
	}
	if s.patcher == nil {
		writeErr(w, http.StatusServiceUnavailable, "live template check unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unreadable body")
		return
	}
	var vin VerifyInput
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&vin); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("bad verify input: %v", err))
		return
	}
	out := VerifyRecovery(vin)
	if !out.Recovered {
		writeJSON(w, 422, map[string]interface{}{"recovered": false, "reason": out.Reason})
		return
	}
	// Persisted rollout check: live template must equal the known-good
	// snapshot (struct-form hash on both sides; see BACKLOG hashing debt).
	tmplJSON, _, _, err := s.store.KnownGood(TargetUID(s.cfg.Policy))
	if err != nil {
		writeErr(w, http.StatusConflict, "no known-good snapshot")
		return
	}
	var desired corev1.PodTemplateSpec
	if err := json.Unmarshal([]byte(tmplJSON), &desired); err != nil {
		writeErr(w, http.StatusConflict, "known-good template unreadable")
		return
	}
	_, wantHash, err := TemplateHash(desired)
	if err != nil {
		writeErr(w, http.StatusConflict, "known-good template unhashable")
		return
	}
	live, _, err := s.patcher.Get(s.cfg.Policy.Deployment)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Sprintf("live read: %v", err))
		return
	}
	if live.TemplateHash != wantHash {
		writeJSON(w, 422, map[string]interface{}{"recovered": false,
			"reason": "live template differs from known-good snapshot"})
		return
	}
	if err := s.store.AppendEvent(id, "verified",
		fmt.Sprintf(`{"windows":%d,"reason":%q}`, len(vin.Windows), out.Reason)); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "persistence unavailable")
		return
	}
	if _, err := s.store.Transition(id, StResolved,
		fmt.Sprintf(`{"by":"verify-record","windows":%d}`, len(vin.Windows))); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"recovered": true, "reason": out.Reason})
}
