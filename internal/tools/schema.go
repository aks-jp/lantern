package tools

import "github.com/google/jsonschema-go/jsonschema"

// maxQueryLength bounds the query string accepted from clients.
const maxQueryLength = 500

var timeRanges = []any{"day", "week", "month", "year"}

// inputSchema builds the input schema of a search tool. withCategories adds
// the categories parameter restricted to the configured categories.
func inputSchema(o Options, withCategories bool) *jsonschema.Schema {
	props := map[string]*jsonschema.Schema{
		"query": {
			Type:        "string",
			Description: "The search query. Use concise keywords; quotes and operators such as site:example.com are passed through.",
			MinLength:   ptr(1),
			MaxLength:   ptr(maxQueryLength),
		},
		"language": {
			Type:        "string",
			Description: "Language of the results as a code such as en, de or de-DE, or auto. Defaults to " + o.DefaultLanguage + ".",
			Pattern:     `^(auto|all|[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*)$`,
		},
		"time_range": {
			Type:        "string",
			Description: "Only return results from the last day, week, month or year. Omit it unless the user asks for a period: many search engines do not support this filter and are skipped when it is set, so results can become sparse or empty.",
			Enum:        timeRanges,
		},
		"page": {
			Type:        "integer",
			Description: "Result page, starting at 1. Request the next page only if the first one was not sufficient.",
			Minimum:     ptr(1.0),
			Maximum:     ptr(10.0),
		},
		"max_results": {
			Type:        "integer",
			Description: "Maximum number of results to return.",
			Minimum:     ptr(1.0),
			Maximum:     ptr(float64(o.MaxResults)),
		},
		"safesearch": {
			Type:        "integer",
			Description: "Filter explicit content: 0 off, 1 moderate, 2 strict.",
			Minimum:     ptr(0.0),
			Maximum:     ptr(2.0),
		},
	}
	if withCategories {
		enum := make([]any, len(o.AllowedCategories))
		for i, c := range o.AllowedCategories {
			enum[i] = c
		}
		props["categories"] = &jsonschema.Schema{
			Type:        "array",
			Description: "Search categories. Defaults to " + o.AllowedCategories[0] + ".",
			Items:       &jsonschema.Schema{Type: "string", Enum: enum},
			MinItems:    ptr(1),
			UniqueItems: true,
		}
	}
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           props,
		Required:             []string{"query"},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

// outputSchema is the schema of SearchOutput, generated once.
var outputSchema = func() *jsonschema.Schema {
	s, err := jsonschema.For[SearchOutput](nil)
	if err != nil {
		panic(err)
	}
	return s
}()

func ptr[T any](v T) *T { return &v }
