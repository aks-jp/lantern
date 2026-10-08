// Package config loads and validates Lantern's configuration from environment
// variables and optional secret files.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config holds the validated runtime configuration.
type Config struct {
	SearXNGURL                *url.URL
	SearXNGAPIKey             string
	SearXNGAPIKeyHeader       string
	SearXNGTimeout            time.Duration
	SearXNGInsecureSkipVerify bool

	MCPAPIKeys []string
	Listen     string
	Path       string

	DefaultLanguage   string
	DefaultSafeSearch int
	MaxResults        int
	SnippetMaxChars   int
	AllowedCategories []string

	LogLevel  slog.Level
	LogFormat string
}

// Source abstracts access to the environment and the file system for testing.
type Source struct {
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
}

// OSSource reads from the process environment and the real file system.
var OSSource = Source{Getenv: os.Getenv, ReadFile: os.ReadFile}

var (
	headerNameRE = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~-]+$`)
	languageRE   = regexp.MustCompile(`^(auto|all|[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*)$`)
	categoryRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9 _-]*$`)
)

// ValidLanguage reports whether s is an accepted SearXNG language value.
func ValidLanguage(s string) bool { return languageRE.MatchString(s) }

// Load reads all settings from src, applies defaults and validates them.
// All validation problems are reported together.
func Load(src Source) (*Config, error) {
	l := loader{src: src}
	c := &Config{}

	c.SearXNGURL = l.url("SEARXNG_URL")
	c.SearXNGAPIKey = l.secret("SEARXNG_API_KEY")
	c.SearXNGAPIKeyHeader = l.str("SEARXNG_API_KEY_HEADER", "X-API-Key")
	if !headerNameRE.MatchString(c.SearXNGAPIKeyHeader) {
		l.fail("SEARXNG_API_KEY_HEADER: %q is not a valid HTTP header name", c.SearXNGAPIKeyHeader)
	}
	c.SearXNGTimeout = l.duration("SEARXNG_TIMEOUT", 10*time.Second)
	c.SearXNGInsecureSkipVerify = l.boolean("SEARXNG_INSECURE_SKIP_VERIFY", false)

	c.MCPAPIKeys = l.keys()
	c.Listen = l.str("MCP_LISTEN", ":8080")
	c.Path = l.str("MCP_PATH", "/mcp")
	if !strings.HasPrefix(c.Path, "/") || c.Path == "/" || c.Path == "/healthz" || strings.ContainsAny(c.Path, " ?#") {
		l.fail("MCP_PATH: %q must be an absolute path other than / and /healthz", c.Path)
	}

	c.DefaultLanguage = l.str("DEFAULT_LANGUAGE", "auto")
	if !ValidLanguage(c.DefaultLanguage) {
		l.fail("DEFAULT_LANGUAGE: %q is not a valid language code (e.g. auto, en, de-DE)", c.DefaultLanguage)
	}
	c.DefaultSafeSearch = l.integer("DEFAULT_SAFESEARCH", 1, 0, 2)
	c.MaxResults = l.integer("MAX_RESULTS", 10, 1, 50)
	c.SnippetMaxChars = l.integer("SNIPPET_MAX_CHARS", 300, 50, 5000)
	c.AllowedCategories = l.categories()

	c.LogLevel = l.logLevel()
	c.LogFormat = l.str("LOG_FORMAT", "json")
	if c.LogFormat != "json" && c.LogFormat != "text" {
		l.fail("LOG_FORMAT: %q must be json or text", c.LogFormat)
	}

	if err := errors.Join(l.errs...); err != nil {
		return nil, err
	}
	return c, nil
}

type loader struct {
	src  Source
	errs []error
}

func (l *loader) fail(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

func (l *loader) str(key, def string) string {
	if v := strings.TrimSpace(l.src.Getenv(key)); v != "" {
		return v
	}
	return def
}

func (l *loader) url(key string) *url.URL {
	raw := l.str(key, "")
	if raw == "" {
		l.fail("%s: required", key)
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		l.fail("%s: %q must be an absolute http(s) URL", key, raw)
		return nil
	}
	if u.RawQuery != "" || u.Fragment != "" {
		l.fail("%s: %q must not contain a query or fragment", key, raw)
		return nil
	}
	if u.User != nil {
		l.fail("%s: credentials in the URL are not supported, use SEARXNG_API_KEY", key)
		return nil
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u
}

// secret returns the content of KEY_FILE if set, otherwise the value of KEY.
func (l *loader) secret(key string) string {
	if path := l.str(key+"_FILE", ""); path != "" {
		b, err := l.src.ReadFile(path)
		if err != nil {
			l.fail("%s_FILE: %v", key, err)
			return ""
		}
		v := strings.TrimSpace(string(b))
		if v == "" {
			l.fail("%s_FILE: %s is empty", key, path)
		}
		return v
	}
	return l.str(key, "")
}

// keys merges MCP_API_KEYS (comma separated) and MCP_API_KEYS_FILE (one per
// line, # comments allowed) into a de-duplicated list.
func (l *loader) keys() []string {
	var keys []string
	for k := range strings.SplitSeq(l.src.Getenv("MCP_API_KEYS"), ",") {
		keys = append(keys, strings.TrimSpace(k))
	}
	if path := l.str("MCP_API_KEYS_FILE", ""); path != "" {
		b, err := l.src.ReadFile(path)
		if err != nil {
			l.fail("MCP_API_KEYS_FILE: %v", err)
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			if i := strings.IndexByte(line, '#'); i >= 0 {
				line = line[:i]
			}
			keys = append(keys, strings.TrimSpace(line))
		}
	}
	keys = slices.DeleteFunc(keys, func(k string) bool { return k == "" })
	for _, k := range keys {
		if strings.ContainsAny(k, " \t") {
			l.fail("MCP_API_KEYS: keys must not contain whitespace")
			break
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		l.fail("%s: %q must be a positive duration such as 10s", key, raw)
		return def
	}
	return d
}

func (l *loader) boolean(key string, def bool) bool {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		l.fail("%s: %q must be true or false", key, raw)
		return def
	}
	return b
}

func (l *loader) integer(key string, def, minimum, maximum int) int {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minimum || n > maximum {
		l.fail("%s: %q must be an integer between %d and %d", key, raw, minimum, maximum)
		return def
	}
	return n
}

func (l *loader) categories() []string {
	var cats []string
	for c := range strings.SplitSeq(l.str("ALLOWED_CATEGORIES", "general,news,science,it"), ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || slices.Contains(cats, c) {
			continue
		}
		if !categoryRE.MatchString(c) {
			l.fail("ALLOWED_CATEGORIES: %q is not a valid category name", c)
			continue
		}
		cats = append(cats, c)
	}
	if len(cats) == 0 {
		l.fail("ALLOWED_CATEGORIES: at least one category is required")
	}
	return cats
}

func (l *loader) logLevel() slog.Level {
	raw := l.str("LOG_LEVEL", "info")
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(raw)); err != nil {
		l.fail("LOG_LEVEL: %q must be debug, info, warn or error", raw)
		return slog.LevelInfo
	}
	return lvl
}
