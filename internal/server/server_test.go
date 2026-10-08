package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aks-jp/lantern/internal/searxng"
	"github.com/aks-jp/lantern/internal/tools"
)

// fakeSearXNG serves fixtures by query: "fail-<status>" returns that status.
func fakeSearXNG(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/search" || q.Get("format") != "json" {
			http.NotFound(w, r)
			return
		}
		switch query := q.Get("q"); {
		case strings.HasPrefix(query, "fail-429"):
			w.WriteHeader(http.StatusTooManyRequests)
		case strings.HasPrefix(query, "fail-403"):
			w.WriteHeader(http.StatusForbidden)
		case strings.HasPrefix(query, "fail-json"):
			_, _ = w.Write([]byte("<html>"))
		case q.Get("categories") == "news":
			if q.Get("time_range") != "week" {
				t.Errorf("news_search must default to time_range=week, got %q", q.Get("time_range"))
			}
			serveFixture(t, w, "search_news.json")
		case query == "nothing":
			serveFixture(t, w, "search_empty.json")
		default:
			serveFixture(t, w, "search_general.json")
		}
	}))
}

func serveFixture(t *testing.T, w http.ResponseWriter, name string) {
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Error(err)
		return
	}
	_, _ = w.Write(b)
}

type testEnv struct {
	url string
}

func newTestEnv(t *testing.T, middleware func(http.Handler) http.Handler) testEnv {
	t.Helper()
	upstream := fakeSearXNG(t)
	t.Cleanup(upstream.Close)
	base, _ := url.Parse(upstream.URL)

	logger := slog.New(slog.DiscardHandler)
	s := mcp.NewServer(&mcp.Implementation{Name: "lantern", Version: "test"}, nil)
	tools.Register(s, searxng.New(searxng.Options{BaseURL: base, Timeout: 2 * time.Second, UserAgent: "lantern/test"}), tools.Options{
		DefaultLanguage:   "auto",
		DefaultSafeSearch: 1,
		MaxResults:        10,
		SnippetMaxChars:   300,
		AllowedCategories: []string{"general", "news"},
		Logger:            logger,
	})
	srv := httptest.NewServer(NewHandler(Options{Path: "/mcp", MCP: s, Logger: logger, Middleware: middleware}))
	t.Cleanup(srv.Close)
	return testEnv{url: srv.URL}
}

func connect(t *testing.T, endpoint string, client *http.Client) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: client, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callText(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return res, b.String()
}

func TestListTools(t *testing.T) {
	session := connect(t, newTestEnv(t, nil).url+"/mcp", nil)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 2 || res.Tools[0].Name != "news_search" && res.Tools[1].Name != "news_search" {
		t.Fatalf("tools = %+v", res.Tools)
	}
	for _, tool := range res.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be annotated read-only", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s must declare an output schema", tool.Name)
		}
		schema, _ := json.Marshal(tool.InputSchema)
		hasCategories := strings.Contains(string(schema), `"categories"`)
		if hasCategories != (tool.Name == "web_search") {
			t.Errorf("%s: categories parameter present = %v", tool.Name, hasCategories)
		}
		if tool.Name == "web_search" && !strings.Contains(string(schema), `"enum":["general","news"]`) {
			t.Errorf("categories enum must list the allowed categories: %s", schema)
		}
	}
}

func TestWebSearch(t *testing.T) {
	session := connect(t, newTestEnv(t, nil).url+"/mcp", nil)
	res, text := callText(t, session, "web_search", map[string]any{"query": "golang context"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "1. context package") || !strings.Contains(text, "Related searches:") {
		t.Errorf("text content:\n%s", text)
	}
	var out tools.SearchOutput
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 3 || out.Query != "golang context" || out.Page != 1 {
		t.Errorf("structured content = %+v", out)
	}
}

func TestNewsSearch(t *testing.T) {
	session := connect(t, newTestEnv(t, nil).url+"/mcp", nil)
	res, text := callText(t, session, "news_search", map[string]any{"query": "open source"})
	if res.IsError || !strings.Contains(text, "[Source: bing news, duckduckgo news · 2026-10-07]") {
		t.Errorf("news text:\n%s", text)
	}
}

func TestToolErrors(t *testing.T) {
	session := connect(t, newTestEnv(t, nil).url+"/mcp", nil)
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"rate limit", map[string]any{"query": "fail-429"}, "rate limit"},
		{"rejected", map[string]any{"query": "fail-403"}, "rejected the request"},
		{"bad json", map[string]any{"query": "fail-json"}, "unexpected response"},
		{"empty query", map[string]any{"query": ""}, "query"},
		{"unknown category", map[string]any{"query": "x", "categories": []string{"images"}}, "categories"},
		{"bad time range", map[string]any{"query": "x", "time_range": "decade"}, "time_range"},
		{"max results too high", map[string]any{"query": "x", "max_results": 11}, "max_results"},
		{"bad language", map[string]any{"query": "x", "language": "<script>"}, "language"},
		{"unknown parameter", map[string]any{"query": "x", "foo": 1}, "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, text := callText(t, session, "web_search", tt.args)
			if !res.IsError || !strings.Contains(text, tt.want) {
				t.Errorf("want tool error containing %q, got isError=%v %q", tt.want, res.IsError, text)
			}
		})
	}
	// The server must keep working after errors.
	if res, _ := callText(t, session, "web_search", map[string]any{"query": "nothing"}); res.IsError {
		t.Error("server unusable after errors")
	}
}

func TestNoResults(t *testing.T) {
	session := connect(t, newTestEnv(t, nil).url+"/mcp", nil)
	res, text := callText(t, session, "web_search", map[string]any{"query": "nothing"})
	if res.IsError || !strings.HasPrefix(text, `No results found for "nothing"`) {
		t.Errorf("got isError=%v %q", res.IsError, text)
	}
}

func TestRoutes(t *testing.T) {
	env := newTestEnv(t, nil)
	tests := []struct {
		method, path string
		status       int
		body         string
	}{
		{"GET", "/healthz", 200, `{"status":"ok"}`},
		{"GET", "/", 404, `"not_found"`},
		{"GET", "/config", 404, `"not_found"`},
		{"POST", "/healthz", 405, ""},
	}
	for _, tt := range tests {
		req, _ := http.NewRequestWithContext(context.Background(), tt.method, env.url+tt.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tt.status || !strings.Contains(string(b), tt.body) {
			t.Errorf("%s %s = %d %s", tt.method, tt.path, resp.StatusCode, b)
		}
	}
}
