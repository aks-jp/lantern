package config

import (
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func source(env map[string]string, files map[string]string) Source {
	return Source{
		Getenv: func(k string) string { return env[k] },
		ReadFile: func(p string) ([]byte, error) {
			if c, ok := files[p]; ok {
				return []byte(c), nil
			}
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
		},
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(source(map[string]string{"SEARXNG_URL": "http://searxng:8080/"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SearXNGURL.String(); got != "http://searxng:8080" {
		t.Errorf("url = %q, trailing slash should be trimmed", got)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"header", c.SearXNGAPIKeyHeader, "X-API-Key"},
		{"timeout", c.SearXNGTimeout, 10 * time.Second},
		{"insecure", c.SearXNGInsecureSkipVerify, false},
		{"keys", len(c.MCPAPIKeys), 0},
		{"listen", c.Listen, ":8080"},
		{"path", c.Path, "/mcp"},
		{"language", c.DefaultLanguage, "auto"},
		{"safesearch", c.DefaultSafeSearch, 1},
		{"max results", c.MaxResults, 10},
		{"snippet", c.SnippetMaxChars, 300},
		{"categories", strings.Join(c.AllowedCategories, ","), "general,news,science,it"},
		{"log level", c.LogLevel, slog.LevelInfo},
		{"log format", c.LogFormat, "json"},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %v, want %v", ch.name, ch.got, ch.want)
		}
	}
}

func TestLoadOverrides(t *testing.T) {
	env := map[string]string{
		"SEARXNG_URL":                  "https://search.example.com/searx",
		"SEARXNG_API_KEY":              "from-env",
		"SEARXNG_API_KEY_HEADER":       "X-Search-Token",
		"SEARXNG_TIMEOUT":              "2500ms",
		"SEARXNG_INSECURE_SKIP_VERIFY": "true",
		"MCP_LISTEN":                   "127.0.0.1:9000",
		"MCP_PATH":                     "/search-mcp",
		"DEFAULT_LANGUAGE":             "de-DE",
		"DEFAULT_SAFESEARCH":           "0",
		"MAX_RESULTS":                  "20",
		"SNIPPET_MAX_CHARS":            "120",
		"ALLOWED_CATEGORIES":           " News, it ,news,social media",
		"LOG_LEVEL":                    "debug",
		"LOG_FORMAT":                   "text",
	}
	c, err := Load(source(env, nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.SearXNGURL.Path != "/searx" || c.SearXNGAPIKey != "from-env" || c.SearXNGAPIKeyHeader != "X-Search-Token" ||
		c.SearXNGTimeout != 2500*time.Millisecond || !c.SearXNGInsecureSkipVerify || c.Listen != "127.0.0.1:9000" ||
		c.Path != "/search-mcp" || c.DefaultLanguage != "de-DE" || c.DefaultSafeSearch != 0 || c.MaxResults != 20 ||
		c.SnippetMaxChars != 120 || c.LogLevel != slog.LevelDebug || c.LogFormat != "text" {
		t.Errorf("unexpected config: %+v", c)
	}
	if got := strings.Join(c.AllowedCategories, ","); got != "news,it,social media" {
		t.Errorf("categories = %q", got)
	}
}

func TestSecretFiles(t *testing.T) {
	env := map[string]string{
		"SEARXNG_URL":          "http://searxng:8080",
		"SEARXNG_API_KEY":      "from-env",
		"SEARXNG_API_KEY_FILE": "/run/secrets/searxng",
		"MCP_API_KEYS":         "key-b, key-a,,",
		"MCP_API_KEYS_FILE":    "/run/secrets/mcp",
	}
	files := map[string]string{
		"/run/secrets/searxng": "from-file\n",
		"/run/secrets/mcp":     "# team keys\nkey-c\n\n  key-a  # duplicate\nkey-d#inline\n",
	}
	c, err := Load(source(env, files))
	if err != nil {
		t.Fatal(err)
	}
	if c.SearXNGAPIKey != "from-file" {
		t.Errorf("file must take precedence, got %q", c.SearXNGAPIKey)
	}
	if got := strings.Join(c.MCPAPIKeys, ","); got != "key-a,key-b,key-c,key-d" {
		t.Errorf("keys = %q", got)
	}
}

func TestLoadErrors(t *testing.T) {
	base := func() map[string]string { return map[string]string{"SEARXNG_URL": "http://searxng:8080"} }
	tests := []struct {
		name string
		key  string
		val  string
		want string
	}{
		{"missing url", "SEARXNG_URL", "", "SEARXNG_URL: required"},
		{"relative url", "SEARXNG_URL", "searxng:8080", "absolute http(s) URL"},
		{"ftp url", "SEARXNG_URL", "ftp://searxng", "absolute http(s) URL"},
		{"url with query", "SEARXNG_URL", "http://searxng/?a=b", "query or fragment"},
		{"url with credentials", "SEARXNG_URL", "http://u:p@searxng", "credentials"},
		{"header", "SEARXNG_API_KEY_HEADER", "X API Key", "valid HTTP header name"},
		{"timeout", "SEARXNG_TIMEOUT", "10", "positive duration"},
		{"negative timeout", "SEARXNG_TIMEOUT", "-1s", "positive duration"},
		{"bool", "SEARXNG_INSECURE_SKIP_VERIFY", "yes", "true or false"},
		{"missing key file", "SEARXNG_API_KEY_FILE", "/nope", "SEARXNG_API_KEY_FILE"},
		{"empty key file", "SEARXNG_API_KEY_FILE", "/empty", "is empty"},
		{"missing mcp key file", "MCP_API_KEYS_FILE", "/nope", "MCP_API_KEYS_FILE"},
		{"key with space", "MCP_API_KEYS", "a b", "whitespace"},
		{"path", "MCP_PATH", "mcp", "absolute path"},
		{"healthz path", "MCP_PATH", "/healthz", "absolute path"},
		{"language", "DEFAULT_LANGUAGE", "german!", "valid language code"},
		{"safesearch", "DEFAULT_SAFESEARCH", "3", "between 0 and 2"},
		{"max results", "MAX_RESULTS", "0", "between 1 and 50"},
		{"max results nan", "MAX_RESULTS", "ten", "between 1 and 50"},
		{"snippet", "SNIPPET_MAX_CHARS", "10", "between 50 and 5000"},
		{"category", "ALLOWED_CATEGORIES", "general,web/news", "valid category"},
		{"no category", "ALLOWED_CATEGORIES", " , ", "at least one"},
		{"log level", "LOG_LEVEL", "verbose", "LOG_LEVEL"},
		{"log format", "LOG_FORMAT", "xml", "json or text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := base()
			env[tt.key] = tt.val
			_, err := Load(source(env, map[string]string{"/empty": "  \n"}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := Load(source(map[string]string{"MAX_RESULTS": "0", "LOG_FORMAT": "xml"}, nil))
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 3 {
		t.Fatalf("want 3 joined errors, got %v", err)
	}
}

func TestValidLanguage(t *testing.T) {
	for _, s := range []string{"auto", "all", "en", "de-DE", "zh-Hant-TW"} {
		if !ValidLanguage(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"", "e", "de_DE", "de-", "<script>"} {
		if ValidLanguage(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}
