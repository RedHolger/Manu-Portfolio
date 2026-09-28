// Package main — cmd/labgateway: public proxy + token-gated admin port.
//
// Public :8080 → POST /v1/reservations (proxied, observed).
// Admin 127.0.0.1:8082 (LAB_ADMIN_TOKEN) → state/routing/faults.
// Restart state: zero faults, zero candidate traffic (§4.2).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sre-portfolio/internal/gateway"
)

func main() {
	var (
		pub       = flag.String("addr", ":8080", "public listen address")
		admin     = flag.String("admin-addr", "127.0.0.1:8082", "admin listen (localhost only)")
		stable    = flag.String("stable", "http://127.0.0.1:8081", "stable API base URL")
		candidate = flag.String("candidate", "http://127.0.0.1:8083", "candidate API base URL")
	)
	flag.Parse()
	token := os.Getenv("LAB_ADMIN_TOKEN")
	if token == "" {
		slog.Error("LAB_ADMIN_TOKEN must be set (uncommitted secret)")
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	gw := gateway.New(map[string]string{"stable": *stable, "candidate": *candidate})
	// Bounded label init (§5.3): zero-error counts exist as series from
	// the first scrape; missing series afterwards = missing telemetry.
	gw.InitSeries(
		[]string{"stable", "candidate"},
		[]string{"/v1/reservations"},
		[]string{"success", "client_error", "server_error", "timeout", "transport_error"},
	)

	pubMux := http.NewServeMux()
	pubMux.HandleFunc("POST /v1/reservations", func(w http.ResponseWriter, r *http.Request) {
		opID := r.Header.Get("X-Operation-ID")
		if opID == "" {
			opID = r.Header.Get("Idempotency-Key")
		}
		if rid := r.Header.Get("X-Request-ID"); rid != "" {
			w.Header().Set("X-Request-ID", rid)
		}
		gw.ServeReserve(w, r, opID, "/v1/reservations")
	})
	pubMux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(gw.Exposition("reservations")))
	})

	adm := &adminAPI{gw: gw, token: token, log: log}
	admMux := http.NewServeMux()
	admMux.HandleFunc("GET /admin/state", adm.authed(adm.handleState))
	admMux.HandleFunc("PUT /admin/routing", adm.authed(adm.handleRouting))
	admMux.HandleFunc("PUT /admin/faults/{id}", adm.authed(adm.handlePutFault))
	admMux.HandleFunc("DELETE /admin/faults/{id}", adm.authed(adm.handleDelFault))
	admMux.HandleFunc("DELETE /admin/faults", adm.authed(adm.handleDelAll))

	mkSrv := func(addr string, h http.Handler) *http.Server {
		return &http.Server{
			Addr: addr, Handler: h,
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
			WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
		}
	}
	pubSrv, admSrv := mkSrv(*pub, pubMux), mkSrv(*admin, admMux)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = pubSrv.Shutdown(shut)
		_ = admSrv.Shutdown(shut)
	}()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				gw.Sweep()
			}
		}
	}()
	go func() {
		if err := admSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("admin exit", "err", err)
			os.Exit(1)
		}
	}()
	log.Info("labgateway listening", "public", *pub, "admin", *admin)
	if err := pubSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("public exit", "err", err)
		os.Exit(1)
	}
}

type adminAPI struct {
	gw    *gateway.Gateway
	token string
	log   *slog.Logger
}

func (a *adminAPI) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if "Bearer "+a.token != strings.TrimSpace(r.Header.Get("Authorization")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (a *adminAPI) handleState(w http.ResponseWriter, _ *http.Request) {
	cfg, faults := a.gw.State()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"routingVersion": cfg.Version, "candidatePercent": cfg.CandidatePercent,
		"faults": faults,
	})
}

func (a *adminAPI) handleRouting(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Version          uint64 `json:"version"`
		CandidatePercent int    `json:"candidatePercent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	cfg, ok := a.gw.SetRouting(b.Version, b.CandidatePercent)
	if !ok {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "stale version or invalid percent",
			"state": map[string]any{"version": cfg.Version},
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": cfg.Version, "candidatePercent": cfg.CandidatePercent,
	})
}

func (a *adminAPI) handlePutFault(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Kind              string  `json:"kind"`
		Slot              string  `json:"slot"`
		DelayMilliseconds int64   `json:"delayMilliseconds"`
		Fraction          float64 `json:"fraction"`
		TTLSeconds        int64   `json:"ttlSeconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ok := a.gw.PutFault(gateway.Fault{
		ID: r.PathValue("id"), Kind: b.Kind, Slot: b.Slot,
		Delay:    time.Duration(b.DelayMilliseconds) * time.Millisecond,
		Fraction: b.Fraction,
	}, time.Duration(b.TTLSeconds)*time.Second)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid fault (kind/slot/fraction/ttl<=120s required)",
		})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *adminAPI) handleDelFault(w http.ResponseWriter, r *http.Request) {
	a.gw.ClearFault(r.PathValue("id")) // idempotent
	w.WriteHeader(http.StatusOK)
}

func (a *adminAPI) handleDelAll(w http.ResponseWriter, _ *http.Request) {
	a.gw.ClearAll() // emergency lab cleanup only
	w.WriteHeader(http.StatusOK)
}
