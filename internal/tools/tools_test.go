package tools

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/aks-jp/lantern/internal/searxng"
)

type fakeSearcher struct {
	got  searxng.Query
	resp *searxng.Response
	err  error
}

func (f *fakeSearcher) Search(_ context.Context, q searxng.Query) (*searxng.Response, error) {
	f.got = q
	return f.resp, f.err
}

func testOptions(s Searcher) Options {
	return Options{
		DefaultLanguage:   "de-DE",
		DefaultSafeSearch: 1,
		MaxResults:        5,
		SnippetMaxChars:   300,
		AllowedCategories: []string{"general", "news", "it"},
		Logger:            slog.New(slog.DiscardHandler),
		searcher:          s,
	}
}

func TestQueryDefaults(t *testing.T) {
	o := testOptions(nil)
	q, err := o.query(SearchInput{Query: "  hello  "}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if q.Q != "hello" || !slices.Equal(q.Categories, []string{"general"}) || q.Language != "de-DE" ||
		q.TimeRange != "" || q.Page != 1 || q.SafeSearch != 1 {
		t.Errorf("unexpected defaults: %+v", q)
	}
}

func TestQueryOverrides(t *testing.T) {
	zero := 0
	q, err := testOptions(nil).query(SearchInput{
		Query: "x", Categories: []string{"it", "news"}, Language: "en", TimeRange: "day", Page: 3, SafeSearch: &zero,
	}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(q.Categories, []string{"it", "news"}) || q.Language != "en" || q.TimeRange != "day" || q.Page != 3 || q.SafeSearch != 0 {
		t.Errorf("overrides not applied: %+v", q)
	}
}

func TestQueryNews(t *testing.T) {
	q, err := testOptions(nil).query(SearchInput{Query: "x", Categories: []string{"it"}}, []string{"news"}, "week")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(q.Categories, []string{"news"}) || q.TimeRange != "week" {
		t.Errorf("news defaults not applied: %+v", q)
	}
	q, _ = testOptions(nil).query(SearchInput{Query: "x", TimeRange: "year"}, []string{"news"}, "week")
	if q.TimeRange != "year" {
		t.Errorf("time_range override ignored: %+v", q)
	}
}

func TestQueryValidation(t *testing.T) {
	o := testOptions(nil)
	if _, err := o.query(SearchInput{Query: "   "}, nil, ""); err == nil {
		t.Error("blank query must fail")
	}
	if _, err := o.query(SearchInput{Query: "x", Categories: []string{"images"}}, nil, ""); err == nil || !strings.Contains(err.Error(), "allowed: general, news, it") {
		t.Errorf("unknown category must fail with the allowed list, got %v", err)
	}
}

func TestHandlerMaxResults(t *testing.T) {
	resp := loadFixture(t, "search_general.json")
	f := &fakeSearcher{resp: resp}
	h := testOptions(f).handler("web_search", nil, "")

	_, out, err := h(context.Background(), nil, SearchInput{Query: "x", MaxResults: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 {
		t.Errorf("want 2 results, got %d", len(out.Results))
	}
	_, out, _ = h(context.Background(), nil, SearchInput{Query: "x", MaxResults: 50})
	if len(out.Results) != 3 {
		t.Errorf("max_results above the limit must be capped, got %d", len(out.Results))
	}
}

func TestHandlerUpstreamError(t *testing.T) {
	f := &fakeSearcher{err: &searxng.Error{Kind: searxng.KindRateLimited, Status: 429, Err: errors.New("x")}}
	res, _, err := testOptions(f).handler("web_search", nil, "")(context.Background(), nil, SearchInput{Query: "x"})
	if res != nil || err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("want rate limit tool error, got %v %v", res, err)
	}
}
