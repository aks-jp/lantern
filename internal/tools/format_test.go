package tools

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aks-jp/lantern/internal/searxng"
)

func loadFixture(t *testing.T, name string) *searxng.Response {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var r searxng.Response
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestBuildOutput(t *testing.T) {
	out := buildOutput("golang context", 1, loadFixture(t, "search_general.json"), 10, 80)

	if len(out.Results) != 3 {
		t.Fatalf("want 3 results after de-duplication, got %d: %+v", len(out.Results), out.Results)
	}
	first := out.Results[0]
	if first.URL != "https://pkg.go.dev/context" || strings.Join(first.Engines, ",") != "duckduckgo,brave,startpage,bing" {
		t.Errorf("duplicate engines must be merged into the first hit: %+v", first)
	}
	if utf8.RuneCountInString(first.Snippet) > 80 || !strings.HasSuffix(first.Snippet, "…") {
		t.Errorf("snippet not truncated: %q", first.Snippet)
	}
	if out.Results[1].PublishedDate != "2014-07-29" || strings.Contains(out.Results[1].Snippet, "  ") {
		t.Errorf("date/whitespace not normalised: %+v", out.Results[1])
	}
	if out.Results[2].Title != "example.org" || out.Results[2].Snippet != "" {
		t.Errorf("empty title must fall back to host: %+v", out.Results[2])
	}
	if len(out.Answers) != 1 || out.Answers[0].URL != "https://go.dev" {
		t.Errorf("answers = %+v", out.Answers)
	}
	if len(out.Infoboxes) != 1 || out.Infoboxes[0].URL != "https://go.dev" {
		t.Errorf("infoboxes = %+v", out.Infoboxes)
	}
	if len(out.Suggestions) != 2 || out.EstimatedTotal != 1250000 {
		t.Errorf("suggestions/total = %v %d", out.Suggestions, out.EstimatedTotal)
	}
}

func TestBuildOutputMaxResults(t *testing.T) {
	out := buildOutput("q", 1, loadFixture(t, "search_general.json"), 1, 300)
	if len(out.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(out.Results))
	}
	if !strings.Contains(strings.Join(out.Results[0].Engines, ","), "bing") {
		t.Error("duplicates beyond the limit must still merge their engines")
	}
}

func TestFormatText(t *testing.T) {
	text := formatText(buildOutput("golang context", 1, loadFixture(t, "search_general.json"), 10, 300))
	order := []string{
		`Search results for "golang context" (page 1):`,
		"Direct answers:\n- Go is a statically typed, compiled programming language. (https://go.dev)",
		"Infobox: Go (programming language)",
		"1. context package - context - Go Packages\n   https://pkg.go.dev/context\n   Package context defines",
		"[Source: duckduckgo, brave, startpage, bing]",
		"2. Go Concurrency Patterns: Context",
		"[Source: brave · 2014-07-29]",
		"3. example.org\n   https://example.org/no-content\n   [Source: mojeek]",
		"Related searches: golang context timeout; golang context withcancel",
	}
	pos := 0
	for _, want := range order {
		i := strings.Index(text[pos:], want)
		if i < 0 {
			t.Fatalf("missing or out of order: %q\n---\n%s", want, text)
		}
		pos += i + len(want)
	}
}

func TestFormatTextNoResults(t *testing.T) {
	text := formatText(buildOutput("xqzv nothing", 1, loadFixture(t, "search_empty.json"), 10, 300))
	if !strings.HasPrefix(text, `No results found for "xqzv nothing"`) || !strings.Contains(text, "Related searches: xqzv") {
		t.Errorf("unexpected text:\n%s", text)
	}
}

func TestFormatTextAnswerOnly(t *testing.T) {
	text := formatText(buildOutput("1+1", 1, loadFixture(t, "search_legacy_answers.json"), 10, 300))
	if !strings.Contains(text, "Direct answers:\n- 2\n") || strings.Contains(text, "No results") {
		t.Errorf("unexpected text:\n%s", text)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		limit int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"the quick brown fox jumps", 20, "the quick brown fox…"},
		{"abcdefghijklmnopqrstuvwxyz", 10, "abcdefghi…"},
		{"äöüäöüäöüäöü", 5, "äöüä…"},
		{"ends with comma, and more words", 17, "ends with comma…"},
	}
	for _, tt := range tests {
		if got := truncate(tt.in, tt.limit); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
		}
		if got := truncate(tt.in, tt.limit); utf8.RuneCountInString(got) > tt.limit {
			t.Errorf("truncate(%q, %d) exceeds limit", tt.in, tt.limit)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	same := [][]string{
		{"https://example.com/a", "http://www.example.com/a/", "https://EXAMPLE.com:443/a#top"},
		{"https://example.com/?b=2&a=1", "https://example.com?a=1&b=2&utm_source=x&UTM_medium=y"},
	}
	for _, group := range same {
		for _, u := range group[1:] {
			if normalizeURL(u) != normalizeURL(group[0]) {
				t.Errorf("%q and %q should collide: %q vs %q", group[0], u, normalizeURL(group[0]), normalizeURL(u))
			}
		}
	}
	different := [][2]string{
		{"https://example.com/a", "https://example.com/b"},
		{"https://example.com/a", "https://example.com:8443/a"},
		{"https://example.com/?a=1", "https://example.com/?a=2"},
		{"https://example.com/A", "https://example.com/a"},
	}
	for _, d := range different {
		if normalizeURL(d[0]) == normalizeURL(d[1]) {
			t.Errorf("%q and %q must not collide", d[0], d[1])
		}
	}
}
