// Package server wires the HTTP routes and runs the HTTP server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ShutdownTimeout is the grace period for in-flight requests on shutdown.
const ShutdownTimeout = 10 * time.Second

// Options configures the HTTP handler.
type Options struct {
	Path   string
	MCP    *mcp.Server
	Logger *slog.Logger
	// Middleware wraps the MCP endpoint, e.g. for authentication. Optional.
	Middleware func(http.Handler) http.Handler
}

// NewHandler returns the root handler: the MCP endpoint at o.Path,
// GET /healthz, and a JSON 404 for everything else.
func NewHandler(o Options) http.Handler {
	mcpHandler := http.Handler(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return o.MCP },
		&mcp.StreamableHTTPOptions{
			// Each request is self-contained; no session state is kept, so
			// the server scales horizontally and survives restarts.
			Stateless:    true,
			JSONResponse: true,
			Logger:       o.Logger,
		},
	))
	if o.Middleware != nil {
		mcpHandler = o.Middleware(mcpHandler)
	}

	mux := http.NewServeMux()
	mux.Handle(o.Path, mcpHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	})
	return mux
}

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// New returns an http.Server with conservative timeouts. upstreamTimeout is
// the SearXNG timeout; responses may take that long plus some margin.
func New(addr string, h http.Handler, upstreamTimeout time.Duration, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      upstreamTimeout + 30*time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

// Run serves until ctx is canceled, then shuts down gracefully within
// ShutdownTimeout.
func Run(ctx context.Context, srv *http.Server, logger *slog.Logger) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", srv.Addr)
	if err != nil {
		return err
	}
	logger.Info("listening", slog.String("addr", ln.Addr().String()))

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down", slog.Duration("timeout", ShutdownTimeout))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("stopped")
	return nil
}
