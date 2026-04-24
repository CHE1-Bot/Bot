// Package httpsrv exposes /healthz and /readyz endpoints so container
// orchestrators (k8s, fly.io, ...) can probe the bot's liveness and
// readiness. Liveness is always 200 once the server is listening;
// readiness reflects whether Discord is connected.
package httpsrv

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

type Health struct {
	ready atomic.Bool
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
