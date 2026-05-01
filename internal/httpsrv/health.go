// Package httpsrv exposes /healthz, /readyz, and /api/meta so container
// orchestrators (k8s, fly.io, ...) can probe the bot, and so the
// Dashboard SPA can introspect its deployment state — mirroring the
// Worker's and Dashboard's /api/meta shape.
package httpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Meta describes the runtime knobs surfaced via /api/meta. Mirrors the
// Worker's Meta and the Dashboard's GET /api/meta response shape.
type Meta struct {
	AppEnv             string
	Version            string
	WorkerConfigured   bool
	DashboardConfigured bool
	DBEnabled          bool
}

type Health struct {
	ready atomic.Bool
	meta  Meta
}

func NewHealth(meta Meta) *Health {
	return &Health{meta: meta}
}

// MarkReady flips readiness to 200. Call after Discord is connected and
// the bot has finished its startup sequence.
func (h *Health) MarkReady() { h.ready.Store(true) }

// MarkNotReady flips readiness to 503. Call during shutdown so the probe
// pulls the pod from rotation before we terminate.
func (h *Health) MarkNotReady() { h.ready.Store(false) }

// Serve runs the health HTTP server on addr until ctx is cancelled.
// Returns non-nil only on unexpected failure; ctx-triggered shutdown
// returns nil.
func Serve(ctx context.Context, addr string, h *Health) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if h.ready.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	})
	mux.HandleFunc("/api/meta", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service":              "bot",
			"app_env":              h.meta.AppEnv,
			"version":              h.meta.Version,
			"db_enabled":           h.meta.DBEnabled,
			"worker_configured":    h.meta.WorkerConfigured,
			"dashboard_configured": h.meta.DashboardConfigured,
			"ready":                h.ready.Load(),
			"server_time":          time.Now().UTC().Format(time.RFC3339),
		})
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("health server listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
