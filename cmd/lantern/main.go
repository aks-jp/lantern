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

	"github.com/aks-jp/lantern/internal/auth"
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
	logStartup(cfg, logger)

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
		KeyID:             auth.HeaderKeyID,
	})

	var authenticator auth.Authenticator
	if len(cfg.MCPAPIKeys) > 0 {
		authenticator = auth.NewStaticKeys(cfg.MCPAPIKeys)
	}
	h := server.NewHandler(server.Options{
		Path:       cfg.Path,
		MCP:        mcpServer,
		Logger:     logger,
		Middleware: auth.Middleware(authenticator, logger),
	})
	return server.New(cfg.Listen, h, cfg.SearXNGTimeout, logger)
}

func newLogger(w io.Writer, cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// logStartup logs the effective configuration without secrets and warns about
// insecure settings.
func logStartup(cfg *config.Config, logger *slog.Logger) {
	keyIDs := make([]string, len(cfg.MCPAPIKeys))
	for i, k := range cfg.MCPAPIKeys {
		keyIDs[i] = auth.KeyID(k)
	}
	logger.Info("starting lantern",
		slog.String("version", version),
		slog.String("searxng_url", cfg.SearXNGURL.Redacted()),
		slog.Bool("searxng_api_key", cfg.SearXNGAPIKey != ""),
		slog.String("searxng_api_key_header", cfg.SearXNGAPIKeyHeader),
		slog.Duration("searxng_timeout", cfg.SearXNGTimeout),
		slog.String("listen", cfg.Listen),
		slog.String("path", cfg.Path),
		slog.Any("mcp_key_ids", keyIDs),
		slog.Any("categories", cfg.AllowedCategories),
		slog.String("default_language", cfg.DefaultLanguage),
		slog.Int("max_results", cfg.MaxResults),
	)
	if len(cfg.MCPAPIKeys) == 0 {
		logger.Warn("MCP endpoint is reachable WITHOUT authentication; set MCP_API_KEYS or MCP_API_KEYS_FILE unless access is restricted otherwise")
	}
	if cfg.SearXNGInsecureSkipVerify {
		logger.Warn("TLS certificate verification for SearXNG is DISABLED (SEARXNG_INSECURE_SKIP_VERIFY=true); use only for testing")
	}
}
