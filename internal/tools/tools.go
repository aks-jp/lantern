// Package tools registers Lantern's MCP search tools.
package tools

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aks-jp/lantern/internal/searxng"
)

// Searcher runs searches against SearXNG.
type Searcher interface {
	Search(ctx context.Context, q searxng.Query) (*searxng.Response, error)
}

// Options configures the search tools.
type Options struct {
	DefaultLanguage   string
	DefaultSafeSearch int
	MaxResults        int
	SnippetMaxChars   int
	AllowedCategories []string
	Logger            *slog.Logger
	// KeyID returns a non-secret identifier of the caller's credential for
	// logging. It may be nil.
	KeyID func(http.Header) string

	searcher Searcher
}

// SearchInput holds the arguments of both search tools. Categories is
// ignored by news_search.
type SearchInput struct {
	Query      string   `json:"query"`
	Categories []string `json:"categories,omitempty"`
	Language   string   `json:"language,omitempty"`
	TimeRange  string   `json:"time_range,omitempty"`
	Page       int      `json:"page,omitempty"`
	MaxResults int      `json:"max_results,omitempty"`
	SafeSearch *int     `json:"safesearch,omitempty"`
}

const webSearchDescription = `Search the web via a SearXNG metasearch engine and return a ranked list of results with title, URL and a short snippet, plus direct answers and related searches when available.

Use this tool for current events, facts you are unsure about, documentation, prices, people, products or anything that needs an up-to-date source. Cite the URLs you rely on.

Results contain search-engine snippets, not full page contents. Refine the query or request page 2 if the results are not sufficient. For recent news prefer news_search.`

const newsSearchDescription = `Search recent news articles via a SearXNG metasearch engine. Returns headlines with URL, snippet, source engines and publication date when known.

Use this tool for current events and recent developments. By default only results from the last week are returned; set time_range to day, month or year to change that.

Results contain snippets, not full articles.`

// Register adds web_search and, if "news" is an allowed category, news_search
// to the server.
func Register(s *mcp.Server, searcher Searcher, o Options) {
	o.searcher = searcher
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	annotations := &mcp.ToolAnnotations{
		ReadOnlyHint:   true,
		IdempotentHint: true,
		OpenWorldHint:  ptr(true),
	}

	web := &mcp.Tool{
		Name:         "web_search",
		Title:        "Web search",
		Description:  webSearchDescription,
		InputSchema:  inputSchema(o, true, ""),
		OutputSchema: outputSchema,
		Annotations:  annotations,
	}
	mcp.AddTool(s, web, o.handler("web_search", nil, ""))

	if slices.Contains(o.AllowedCategories, "news") {
		news := &mcp.Tool{
			Name:         "news_search",
			Title:        "News search",
			Description:  newsSearchDescription,
			InputSchema:  inputSchema(o, false, "week"),
			OutputSchema: outputSchema,
			Annotations:  annotations,
		}
		mcp.AddTool(s, news, o.handler("news_search", []string{"news"}, "week"))
	} else {
		o.Logger.Info("news_search disabled because \"news\" is not in ALLOWED_CATEGORIES")
	}
}

// handler returns the tool handler. fixedCategories and defaultTimeRange
// specialise it for news_search.
func (o Options) handler(name string, fixedCategories []string, defaultTimeRange string) mcp.ToolHandlerFor[SearchInput, SearchOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		q, err := o.query(in, fixedCategories, defaultTimeRange)
		if err != nil {
			return nil, SearchOutput{}, err
		}
		maxResults := o.MaxResults
		if in.MaxResults > 0 && in.MaxResults < maxResults {
			maxResults = in.MaxResults
		}

		start := time.Now()
		resp, err := o.searcher.Search(ctx, q)
		attrs := []any{
			slog.String("tool", name),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("key_id", o.keyID(req)),
		}
		if o.Logger.Enabled(ctx, slog.LevelDebug) {
			attrs = append(attrs, slog.String("query", q.Q), slog.Any("categories", q.Categories), slog.Int("page", q.Page))
		}

		if err != nil {
			var se *searxng.Error
			if !errors.As(err, &se) {
				se = &searxng.Error{Kind: searxng.KindUnreachable, Err: err}
			}
			attrs = append(attrs, slog.Int("upstream_status", se.Status), slog.String("error", se.Error()))
			o.Logger.Log(ctx, se.Level(), "search failed", attrs...)
			return nil, SearchOutput{}, errors.New(se.Message())
		}

		out := buildOutput(q.Q, q.Page, resp, maxResults, o.SnippetMaxChars)
		attrs = append(attrs, slog.Int("upstream_status", http.StatusOK), slog.Int("hits", len(out.Results)))
		if len(resp.Unresponsive) > 0 {
			attrs = append(attrs, slog.Int("unresponsive_engines", len(resp.Unresponsive)))
		}
		o.Logger.InfoContext(ctx, "tool call", attrs...)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatText(out)}},
		}, out, nil
	}
}

// query validates the input and applies defaults.
func (o Options) query(in SearchInput, fixedCategories []string, defaultTimeRange string) (searxng.Query, error) {
	q := searxng.Query{
		Q:          strings.TrimSpace(in.Query),
		Categories: fixedCategories,
		Language:   o.DefaultLanguage,
		TimeRange:  defaultTimeRange,
		Page:       1,
		SafeSearch: o.DefaultSafeSearch,
	}
	if q.Q == "" {
		return q, errors.New("the query must not be empty")
	}
	if fixedCategories == nil {
		q.Categories = []string{o.AllowedCategories[0]}
		if len(in.Categories) > 0 {
			for _, c := range in.Categories {
				if !slices.Contains(o.AllowedCategories, c) {
					return q, errors.New("unknown category " + c + "; allowed: " + strings.Join(o.AllowedCategories, ", "))
				}
			}
			q.Categories = in.Categories
		}
	}
	if in.Language != "" {
		q.Language = in.Language
	}
	if in.TimeRange != "" {
		q.TimeRange = in.TimeRange
	}
	if in.Page > 0 {
		q.Page = in.Page
	}
	if in.SafeSearch != nil {
		q.SafeSearch = *in.SafeSearch
	}
	return q, nil
}

func (o Options) keyID(req *mcp.CallToolRequest) string {
	if o.KeyID == nil || req == nil || req.Extra == nil {
		return ""
	}
	return o.KeyID(req.Extra.Header)
}
