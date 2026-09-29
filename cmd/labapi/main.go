// cmd/labapi — reservation API (§4.1).
// Store selection: DATABASE_URL set → PostgresStore (real transactions);
// unset → MemStore (local runs). The pg driver import stays here (binary
// edge) so internal/workload remains driver-agnostic until versions pin it).
//
// Endpoints: POST /v1/reservations, GET /v1/reservations/{id},
// GET /healthz (no DB), GET /readyz (bounded DB check), GET /metrics.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"sre-portfolio/internal/workload"

	_ "github.com/jackc/pgx/v5/stdlib" // pinned in go.mod (v5.11.0)
)

func main() {
	var (
		addr      = flag.String("addr", ":8081", "listen address")
		adminAddr = flag.String("admin-addr", "", "admin listen (localhost only; empty disables)")
		labMode   = flag.String("mode", "healthy", "healthy|error|slow (LAB_MODE)")
		stock     = flag.Int64("stock", 100000, "seed stock for demo-item")
		slowMs    = flag.Int("slow-ms", 400, "added delay in slow mode")
		errorPct  = flag.Float64("error-rate", 0.05, "seeded pre-tx error rate in error mode")
	)
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var store workload.Store
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			log.Error("pg open", "err", err)
			os.Exit(1)
		}
		db.SetMaxOpenConns(20)
		store = workload.NewPostgresStore(db)
		log.Info("store=postgres")
	} else {
		store = workload.NewMemStore(*stock)
		log.Info("store=mem")
	}
	srv := &server{store: store, log: log, mode: *labMode, slow: time.Duration(*slowMs) * time.Millisecond, errRate: *errorPct, dep: &depFault{}}

	mux := buildMux(srv)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *adminAddr != "" {
		tok := os.Getenv("LAB_ADMIN_TOKEN")
		if tok == "" {
			log.Error("LAB_ADMIN_TOKEN required with -admin-addr")
			os.Exit(1)
		}
		adm := http.NewServeMux()
		adm.HandleFunc("PUT /admin/depfault", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+tok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
			var b struct {
				Fail       bool  `json:"fail"`
				TTLSeconds int64 `json:"ttlSeconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil || (b.Fail && b.TTLSeconds <= 0) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			srv.dep.set(b.Fail, time.Duration(b.TTLSeconds)*time.Second)
			w.WriteHeader(http.StatusOK)
		})
		adm.HandleFunc("GET /admin/depfault", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+tok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]bool{"failing": srv.dep.down()})
		})
		admSrv := &http.Server{Addr: *adminAddr, Handler: adm,
			ReadHeaderTimeout: 5 * time.Second}
		go func() {
			<-ctx.Done()
			shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = admSrv.Shutdown(shut)
		}()
		go func() {
			if err := admSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("admin exit", "err", err)
				os.Exit(1)
			}
		}()
		log.Info("labapi admin listening", "addr", *adminAddr)
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()
	log.Info("labapi listening", "addr", *addr, "mode", *labMode)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server exit", "err", err)
		os.Exit(1)
	}
}

type server struct {
	store   workload.Store
	log     *slog.Logger
	mode    string
	slow    time.Duration
	errRate float64
	dep     *depFault
	// testHooks enables the X-Test-Drop-Response path used by the
	// committed-but-response-lost test. Never enabled in deployment
	// (field defaults false; no flag or env sets it).
	testHooks bool
}

// buildMux wires all routes (shared by main and server-level tests).
func buildMux(srv *server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/reservations", srv.handleReserve)
	mux.HandleFunc("GET /v1/reservations/{id}", srv.handleGet)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", srv.handleReady)
	mux.HandleFunc("GET /metrics", srv.handleMetrics)
	return mux
}

// depFault is a bounded simulated dependency outage (F3): pre-transaction
// 503s that expire monotonically without runner involvement. Admin-gated,
// localhost-only, never exposed via a Service.
type depFault struct {
	mu      sync.Mutex
	fail    bool
	expires time.Time
}

// set arms (fail=true, ttl>0) or disarms the fault.
func (d *depFault) set(fail bool, ttl time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail = fail
	if fail {
		d.expires = time.Now().Add(ttl)
	}
}

// down reports whether the fault is currently active (lazy expiry).
func (d *depFault) down() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.fail {
		return false
	}
	if time.Now().After(d.expires) {
		d.fail = false
		return false
	}
	return true
}

type reserveBody struct {
	SKU      string `json:"sku"`
	Quantity int64  `json:"quantity"`
}

func (s *server) handleReserve(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	if reqID == "" {
		reqID = workload.NewID()
	}
	log := s.log.With("req", reqID)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var b reserveBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeErr(w, http.StatusBadRequest, "Idempotency-Key required")
		return
	}
	// Dependency-outage simulation (F3) fires BEFORE the transaction and
	// BEFORE seeded modes: unavailable dependency, nothing written.
	if s.dep != nil && s.dep.down() {
		writeErr(w, http.StatusServiceUnavailable, workload.ErrDependencyDown.Error())
		return
	}
	// Seeded failure modes occur BEFORE the transaction (§4.1).
	switch s.mode {
	case "slow":
		select {
		case <-time.After(s.slow):
		case <-r.Context().Done():
			return
		}
	case "error":
		if hashReq(reqID)%100 < int(s.errRate*100) {
			log.Info("seeded pre-tx error")
			writeErr(w, http.StatusInternalServerError, "seeded error")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	res, replayed, err := s.store.Reserve(ctx, workload.ReserveRequest{
		SKU: b.SKU, Quantity: b.Quantity, IdempotencyKey: key,
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, workload.ErrInvalidRequest):
			status = http.StatusBadRequest
		case errors.Is(err, workload.ErrUnknownSKU):
			status = http.StatusBadRequest
		case errors.Is(err, workload.ErrInsufficientStock):
			status = http.StatusConflict
		case errors.Is(err, workload.ErrConflictingKey):
			status = http.StatusConflict
		case errors.Is(err, workload.ErrDependencyDown):
			status = http.StatusServiceUnavailable
		case errors.Is(err, context.DeadlineExceeded):
			status = http.StatusServiceUnavailable
		}
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Test-only lost-response path: the reservation above already committed.
	// With testHooks on, drop the connection without writing a response so
	// the client observes an ambiguous outcome (commit landed, reply lost).
	if s.testHooks && r.Header.Get("X-Test-Drop-Response") == "1" {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, herr := hj.Hijack(); herr == nil {
				_ = conn.Close()
				return
			}
		}
		// No hijack available (in-process recorder): fall through and
		// respond normally. The drop path is covered by server-level tests.
	}
	w.WriteHeader(map[bool]int{true: http.StatusOK, false: http.StatusCreated}[replayed])
	_ = json.NewEncoder(w).Encode(map[string]string{"id": res.ID})
	log.Info("reserved", "id", res.ID, "replayed", replayed)
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	res, err := s.store.Get(ctx, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": res.ID, "sku": res.SKU, "quantity": res.Quantity,
	})
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if _, _, err := s.store.Inventory(ctx, "demo-item"); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (s *server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	// Minimal exposition (stdlib only); gateway metrics are authoritative.
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintln(w, "# HELP labapi_up Process liveness (1 = alive).")
	_, _ = fmt.Fprintln(w, "# TYPE labapi_up gauge")
	_, _ = fmt.Fprintln(w, "labapi_up 1")
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// hashReq maps a request ID to 0..99 for deterministic seeded errors.
func hashReq(s string) int {
	h := 0
	for i := 0; i < len(s); i++ {
		h = (h*31 + int(s[i])) % 100
	}
	if h < 0 {
		h += 100
	}
	return h
}
