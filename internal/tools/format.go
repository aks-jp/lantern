package tools

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aks-jp/lantern/internal/searxng"
)

// SearchOutput is the structured result of a search tool.
type SearchOutput struct {
	Query          string        `json:"query" jsonschema:"the query that was searched"`
	Page           int           `json:"page" jsonschema:"result page"`
	Answers        []AnswerItem  `json:"answers" jsonschema:"direct answers, if any"`
	Infoboxes      []InfoboxItem `json:"infoboxes" jsonschema:"knowledge panels, if any"`
	Results        []ResultItem  `json:"results" jsonschema:"search results, de-duplicated by URL"`
	Suggestions    []string      `json:"suggestions" jsonschema:"related search queries"`
	EstimatedTotal int           `json:"estimated_total,omitempty" jsonschema:"estimated total number of results, if reported"`
}

// ResultItem is a single search hit.
type ResultItem struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Snippet       string   `json:"snippet,omitempty" jsonschema:"excerpt from the page, possibly shortened"`
	Engines       []string `json:"engines,omitempty" jsonschema:"search engines that returned this result"`
	PublishedDate string   `json:"published_date,omitempty" jsonschema:"publication date (YYYY-MM-DD), if known"`
}

// AnswerItem is a direct answer.
type AnswerItem struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}

// InfoboxItem is a reduced knowledge panel.
type InfoboxItem struct {
	Title   string `json:"title"`
	Content string `json:"content,omitempty"`
	URL     string `json:"url,omitempty"`
}

var (
	whitespaceRE = regexp.MustCompile(`\s+`)
	dateRE       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
)

// buildOutput converts a SearXNG response into the tool output.
func buildOutput(query string, page int, resp *searxng.Response, maxResults, snippetMax int) SearchOutput {
	out := SearchOutput{
		Query:          query,
		Page:           page,
		Answers:        []AnswerItem{},
		Infoboxes:      []InfoboxItem{},
		Results:        []ResultItem{},
		Suggestions:    []string{},
		EstimatedTotal: int(resp.NumberOfResults),
	}

	for _, a := range resp.Answers {
		if t := clean(a.Text); t != "" {
			out.Answers = append(out.Answers, AnswerItem{Text: truncate(t, snippetMax), URL: a.URL})
		}
	}

	for _, ib := range resp.Infoboxes {
		item := InfoboxItem{Title: clean(ib.Title), Content: truncate(clean(ib.Content), snippetMax)}
		if len(ib.URLs) > 0 {
			item.URL = ib.URLs[0].URL
		} else if strings.HasPrefix(ib.ID, "http") {
			item.URL = ib.ID
		}
		if item.Title != "" {
			out.Infoboxes = append(out.Infoboxes, item)
		}
	}

	seen := map[string]int{}
	for _, r := range resp.Results {
		if r.URL == "" {
			continue
		}
		key := normalizeURL(r.URL)
		engines := r.Engines
		if len(engines) == 0 && r.Engine != "" {
			engines = []string{r.Engine}
		}
		if i, dup := seen[key]; dup {
			for _, e := range engines {
				if !slices.Contains(out.Results[i].Engines, e) {
					out.Results[i].Engines = append(out.Results[i].Engines, e)
				}
			}
			continue
		}
		if len(out.Results) == maxResults {
			continue
		}
		seen[key] = len(out.Results)
		title := clean(r.Title)
		if title == "" {
			title = hostOf(r.URL)
		}
		item := ResultItem{
			Title:   title,
			URL:     r.URL,
			Snippet: truncate(clean(r.Content), snippetMax),
			Engines: slices.Clone(engines),
		}
		if r.PublishedDate != nil && dateRE.MatchString(*r.PublishedDate) {
			item.PublishedDate = (*r.PublishedDate)[:10]
		}
		out.Results = append(out.Results, item)
	}

	for _, s := range resp.Suggestions {
		if s = clean(s); s != "" && !slices.Contains(out.Suggestions, s) {
			out.Suggestions = append(out.Suggestions, s)
		}
	}
	return out
}

// formatText renders the output as compact text for the model.
func formatText(o SearchOutput) string {
	var b strings.Builder
	if len(o.Results) == 0 && len(o.Answers) == 0 && len(o.Infoboxes) == 0 {
		fmt.Fprintf(&b, "No results found for %q (page %d). Try other keywords, fewer filters or a broader time range.\n", o.Query, o.Page)
		writeSuggestions(&b, o.Suggestions)
		return b.String()
	}

	fmt.Fprintf(&b, "Search results for %q (page %d):\n", o.Query, o.Page)
	if len(o.Answers) > 0 {
		b.WriteString("\nDirect answers:\n")
		for _, a := range o.Answers {
			b.WriteString("- " + a.Text)
			if a.URL != "" {
				b.WriteString(" (" + a.URL + ")")
			}
			b.WriteByte('\n')
		}
	}
	for _, ib := range o.Infoboxes {
		b.WriteString("\nInfobox: " + ib.Title + "\n")
		if ib.Content != "" {
			b.WriteString("   " + ib.Content + "\n")
		}
		if ib.URL != "" {
			b.WriteString("   " + ib.URL + "\n")
		}
	}
	if len(o.Results) > 0 {
		b.WriteByte('\n')
	}
	for i, r := range o.Results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		var meta []string
		if len(r.Engines) > 0 {
			meta = append(meta, strings.Join(r.Engines, ", "))
		}
		if r.PublishedDate != "" {
			meta = append(meta, r.PublishedDate)
		}
		line := r.Snippet
		if len(meta) > 0 {
			if line != "" {
				line += "  "
			}
			line += "[Source: " + strings.Join(meta, " · ") + "]"
		}
		if line != "" {
			b.WriteString("   " + line + "\n")
		}
	}
	writeSuggestions(&b, o.Suggestions)
	return b.String()
}

func writeSuggestions(b *strings.Builder, s []string) {
	if len(s) > 0 {
		b.WriteString("\nRelated searches: " + strings.Join(s, "; ") + "\n")
	}
}

// clean collapses whitespace and trims the string.
func clean(s string) string {
	return strings.TrimSpace(whitespaceRE.ReplaceAllString(s, " "))
}

// truncate shortens s to at most limit runes, preferring a word boundary,
// and appends an ellipsis when it cut something.
func truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	cut := string(runes[:limit-1])
	// Drop a partial last word unless the cut falls exactly on a word end.
	if runes[limit-1] != ' ' {
		if i := strings.LastIndexByte(cut, ' '); i > len(cut)*2/3 {
			cut = cut[:i]
		}
	}
	return strings.TrimRight(cut, " ,;:.-") + "…"
}

// normalizeURL returns a key under which equivalent URLs collide: scheme,
// "www.", default ports, fragments, trailing slashes, utm_* parameters and
// parameter order are ignored.
func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		host += ":" + p
	}
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") {
			q.Del(k)
		}
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	key := host + path
	if enc := q.Encode(); enc != "" {
		key += "?" + enc
	}
	return key
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}
