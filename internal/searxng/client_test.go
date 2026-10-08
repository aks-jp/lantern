package searxng

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newClient(t *testing.T, srvURL string, mod func(*Options)) *Client {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{BaseURL: u, APIKeyHeader: "X-API-Key", Timeout: 2 * time.Second, UserAgent: "lantern/test"}
	if mod != nil {
		mod(&opts)
	}
	return New(opts)
}

func TestSearchRequest(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write(fixture(t, "search_general.json"))
	}))
	defer srv.Close()

	c := newClient(t, srv.URL+"/searx/", nil)
	resp, err := c.Search(context.Background(), Query{
		Q: "golang context", Categories: []string{"general", "it"}, Language: "de-DE",
		TimeRange: "month", Page: 2, SafeSearch: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet || got.URL.Path != "/searx/search" {
		t.Errorf("request = %s %s", got.Method, got.URL.Path)
	}
	want := map[string]string{
		"q": "golang context", "format": "json", "categories": "general,it", "language": "de-DE",
		"time_range": "month", "pageno": "2", "safesearch": "0",
	}
	for k, v := range want {
		if g := got.URL.Query().Get(k); g != v {
			t.Errorf("param %s = %q, want %q", k, g, v)
		}
	}
	if got.Header.Get("User-Agent") != "lantern/test" || got.Header.Get("Accept") != "application/json" {
		t.Errorf("headers = %v", got.Header)
	}
	if got.Header.Get("X-API-Key") != "" {
		t.Error("no API key header expected without a key")
	}

	if len(resp.Results) != 4 || resp.Results[0].Engines[1] != "brave" || resp.NumberOfResults != 1250000 {
		t.Errorf("results not decoded: %+v", resp.Results)
	}
	if resp.Results[0].PublishedDate != nil || *resp.Results[2].PublishedDate != "2014-07-29T00:00:00" {
		t.Error("publishedDate not decoded")
	}
	if len(resp.Answers) != 1 || !strings.HasPrefix(resp.Answers[0].Text, "Go is") || resp.Answers[0].URL != "https://go.dev" {
		t.Errorf("answers = %+v", resp.Answers)
	}
	if len(resp.Infoboxes) != 1 || resp.Infoboxes[0].Title != "Go (programming language)" || len(resp.Infoboxes[0].URLs) != 2 {
		t.Errorf("infoboxes = %+v", resp.Infoboxes)
	}
	if len(resp.Suggestions) != 2 || len(resp.Unresponsive) != 1 || resp.Unresponsive[0][0] != "qwant" {
		t.Errorf("suggestions/unresponsive = %v %v", resp.Suggestions, resp.Unresponsive)
	}
}

func TestSearchFirstPageOmitsPageno(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("pageno") || r.URL.Query().Has("time_range") || r.URL.Query().Has("categories") {
			t.Errorf("unexpected params: %s", r.URL.RawQuery)
		}
		_, _ = w.Write(fixture(t, "search_empty.json"))
	}))
	defer srv.Close()
	if _, err := newClient(t, srv.URL, nil).Search(context.Background(), Query{Q: "x", Page: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestSearchAPIKeyHeader(t *testing.T) {
	tests := []struct {
		name, header, key string
	}{
		{"default header", "X-API-Key", "secret-1"},
		{"custom header", "X-Search-Token", "secret-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h = r.Header.Clone()
				_, _ = w.Write(fixture(t, "search_empty.json"))
			}))
			defer srv.Close()
			c := newClient(t, srv.URL, func(o *Options) { o.APIKey, o.APIKeyHeader = tt.key, tt.header })
			if _, err := c.Search(context.Background(), Query{Q: "x"}); err != nil {
				t.Fatal(err)
			}
			if h.Get(tt.header) != tt.key {
				t.Errorf("%s = %q, want %q", tt.header, h.Get(tt.header), tt.key)
			}
			if tt.header != "X-API-Key" && h.Get("X-API-Key") != "" {
				t.Error("default header must not be sent when a custom one is configured")
			}
		})
	}
}

func TestLegacyStringAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "search_legacy_answers.json"))
	}))
	defer srv.Close()
	resp, err := newClient(t, srv.URL, nil).Search(context.Background(), Query{Q: "1+1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) != 1 || resp.Answers[0].Text != "2" {
		t.Errorf("answers = %+v", resp.Answers)
	}
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   Kind
		level  slog.Level
	}{
		{"unauthorized", 401, "", KindRejected, slog.LevelError},
		{"forbidden", 403, "", KindRejected, slog.LevelError},
		{"not found", 404, "", KindNotFound, slog.LevelError},
		{"rate limited", 429, "", KindRateLimited, slog.LevelWarn},
		{"server error", 500, "", KindUnavailable, slog.LevelWarn},
		{"bad gateway", 502, "", KindUnavailable, slog.LevelWarn},
		{"bad request", 400, "", KindBadRequest, slog.LevelWarn},
		{"redirect", 302, "", KindRedirect, slog.LevelError},
		{"invalid json", 200, "<html>not json</html>", KindBadResponse, slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status == 302 {
					w.Header().Set("Location", "https://elsewhere.example/")
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := newClient(t, srv.URL, nil).Search(context.Background(), Query{Q: "x"})
			var se *Error
			if !errors.As(err, &se) {
				t.Fatalf("want *Error, got %v", err)
			}
			if se.Kind != tt.kind || se.Status != tt.status || se.Level() != tt.level {
				t.Errorf("got kind=%v status=%d level=%v", se.Kind, se.Status, se.Level())
			}
			if se.Message() == "" || strings.Contains(se.Message(), srv.URL) {
				t.Errorf("message must be non-empty and free of config details: %q", se.Message())
			}
		})
	}
}

func TestSearchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := newClient(t, srv.URL, func(o *Options) { o.Timeout = 50 * time.Millisecond })
	_, err := c.Search(context.Background(), Query{Q: "x"})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindUnreachable || se.Status != 0 {
		t.Fatalf("want unreachable, got %v", err)
	}
}

func TestSearchUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	_, err := newClient(t, u, nil).Search(context.Background(), Query{Q: "x"})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindUnreachable {
		t.Fatalf("want unreachable, got %v", err)
	}
}

func TestSearchCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newClient(t, srv.URL, nil).Search(ctx, Query{Q: "x"})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindCanceled {
		t.Fatalf("want canceled, got %v", err)
	}
}
