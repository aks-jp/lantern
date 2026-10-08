// Command lantern is an MCP server that exposes a SearXNG instance as web search tools.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aks-jp/lantern/internal/config"
	"github.com/aks-jp/lantern/internal/searxng"
	"github.com/aks-jp/lantern/internal/server"
	"github.com/aks-jp/lantern/internal/tools"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		os.Exit(serve())
	case "version", "--version", "-v":
		fmt.Println("lantern", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: lantern [serve|version]\n", cmd)
		os.Exit(2)
	}
}

func serve() int {
	cfg, err := config.Load(config.OSSource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lantern: invalid configuration:\n%v\n", err)
		return 2
	}
	logger := newLogger(os.Stderr, cfg)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, buildServer(cfg, logger), logger); err != nil {
		logger.Error("server failed", slog.String("error", err.Error()))
		return 1
	}
	return 0
}

func buildServer(cfg *config.Config, logger *slog.Logger) *http.Server {
	client := searxng.New(searxng.Options{
		BaseURL:            cfg.SearXNGURL,
		APIKey:             cfg.SearXNGAPIKey,
		APIKeyHeader:       cfg.SearXNGAPIKeyHeader,
		Timeout:            cfg.SearXNGTimeout,
		InsecureSkipVerify: cfg.SearXNGInsecureSkipVerify,
		UserAgent:          "lantern/" + version,
	})

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "lantern", Title: "Lantern web search", Version: version}, nil)
	tools.Register(mcpServer, client, tools.Options{
		DefaultLanguage:   cfg.DefaultLanguage,
		DefaultSafeSearch: cfg.DefaultSafeSearch,
		MaxResults:        cfg.MaxResults,
		SnippetMaxChars:   cfg.SnippetMaxChars,
		AllowedCategories: cfg.AllowedCategories,
		Logger:            logger,
	})

	h := server.NewHandler(server.Options{Path: cfg.Path, MCP: mcpServer, Logger: logger})
	return server.New(cfg.Listen, h, cfg.SearXNGTimeout, logger)
}

func newLogger(w io.Writer, cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
