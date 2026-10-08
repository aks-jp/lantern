package searxng

import (
	"encoding/json"
	"log/slog"
)

// Response is the subset of the SearXNG JSON response Lantern uses.
// Unknown fields are ignored.
type Response struct {
	Query           string     `json:"query"`
	NumberOfResults float64    `json:"number_of_results"`
	Results         []Result   `json:"results"`
	Answers         []Answer   `json:"answers"`
	Infoboxes       []Infobox  `json:"infoboxes"`
	Suggestions     []string   `json:"suggestions"`
	Unresponsive    [][]string `json:"unresponsive_engines"`
}

// Result is a single search hit.
type Result struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	Engine        string   `json:"engine"`
	Engines       []string `json:"engines"`
	PublishedDate *string  `json:"publishedDate"`
	Category      string   `json:"category"`
	Score         float64  `json:"score"`
}

// Answer is a direct answer. Older SearXNG versions return plain strings,
// newer ones return objects; both are accepted.
type Answer struct {
	Text string
	URL  string
}

// UnmarshalJSON accepts either a string or an object with an "answer" field.
func (a *Answer) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		a.Text = s
		return nil
	}
	var obj struct {
		Answer string `json:"answer"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	a.Text, a.URL = obj.Answer, obj.URL
	return nil
}

// Infobox is a knowledge panel, e.g. from Wikipedia or Wikidata.
type Infobox struct {
	Title   string       `json:"infobox"`
	ID      string       `json:"id"`
	Content string       `json:"content"`
	URLs    []InfoboxURL `json:"urls"`
	Engine  string       `json:"engine"`
}

// InfoboxURL is a link attached to an infobox.
type InfoboxURL struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// UnmarshalJSON tolerates unresponsive_engines entries of mixed type,
// e.g. [["qwant","timeout"]] or [["x", 503]].
func (r *Response) UnmarshalJSON(b []byte) error {
	type plain Response
	var aux struct {
		plain
		Unresponsive json.RawMessage `json:"unresponsive_engines"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*r = Response(aux.plain)
	r.Unresponsive = nil
	var raw [][]any
	if len(aux.Unresponsive) > 0 && json.Unmarshal(aux.Unresponsive, &raw) == nil {
		for _, entry := range raw {
			var parts []string
			for _, p := range entry {
				if s, ok := p.(string); ok {
					parts = append(parts, s)
				}
			}
			r.Unresponsive = append(r.Unresponsive, parts)
		}
	}
	return nil
}

// LogValue keeps response details compact in logs.
func (r *Response) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("results", len(r.Results)),
		slog.Int("answers", len(r.Answers)),
		slog.Int("infoboxes", len(r.Infoboxes)),
		slog.Int("unresponsive_engines", len(r.Unresponsive)),
	)
}
