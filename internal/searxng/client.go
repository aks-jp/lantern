// Package searxng implements a minimal client for the SearXNG JSON search API.
//
// Only GET /search?format=json is used, so the client also works behind a
// reverse proxy that exposes nothing else.
package searxng

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxBodyBytes caps the size of a SearXNG response that will be decoded.
const maxBodyBytes = 8 << 20

// Options configures a Client.
type Options struct {
	BaseURL            *url.URL
	APIKey             string
	APIKeyHeader       string
	Timeout            time.Duration
	InsecureSkipVerify bool
	UserAgent          string
}

// Client sends search requests to SearXNG. It is safe for concurrent use.
type Client struct {
	endpoint  string
	apiKey    string
	keyHeader string
	userAgent string
	http      *http.Client
}

// New creates a Client with a pooled HTTP transport.
func New(opts Options) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 32
	if opts.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit opt-in for test setups
	}
	endpoint := *opts.BaseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/search"
	return &Client{
		endpoint:  endpoint.String(),
		apiKey:    opts.APIKey,
		keyHeader: opts.APIKeyHeader,
		userAgent: opts.UserAgent,
		http: &http.Client{
			Timeout:   opts.Timeout,
			Transport: transport,
			// Never follow redirects: a redirect could leak the API key header
			// to another host and usually means SEARXNG_URL is wrong.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Query describes a single search request.
type Query struct {
	Q          string
	Categories []string
	Language   string
	TimeRange  string
	Page       int
	SafeSearch int
}

func (q Query) values() url.Values {
	v := url.Values{}
	v.Set("q", q.Q)
	v.Set("format", "json")
	if len(q.Categories) > 0 {
		v.Set("categories", strings.Join(q.Categories, ","))
	}
	if q.Language != "" {
		v.Set("language", q.Language)
	}
	if q.TimeRange != "" {
		v.Set("time_range", q.TimeRange)
	}
	if q.Page > 1 {
		v.Set("pageno", strconv.Itoa(q.Page))
	}
	v.Set("safesearch", strconv.Itoa(q.SafeSearch))
	return v
}

// Search runs a query against SearXNG. Failures are returned as *Error.
func (c *Client) Search(ctx context.Context, q Query) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+q.values().Encode(), nil)
	if err != nil {
		return nil, &Error{Kind: KindUnreachable, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.apiKey != "" {
		req.Header.Set(c.keyHeader, c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, classifyTransportError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, statusError(resp.StatusCode)
	}

	var out Response
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes))
	if err := dec.Decode(&out); err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, &Error{Kind: KindUnreachable, Status: resp.StatusCode, Err: err}
		}
		return nil, &Error{Kind: KindBadResponse, Status: resp.StatusCode, Err: err}
	}
	return &out, nil
}

func classifyTransportError(err error) *Error {
	if errors.Is(err, context.Canceled) {
		return &Error{Kind: KindCanceled, Err: err}
	}
	return &Error{Kind: KindUnreachable, Err: err}
}

func statusError(status int) *Error {
	e := &Error{Status: status, Err: fmt.Errorf("unexpected HTTP status %d", status)}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Kind = KindRejected
	case status == http.StatusNotFound:
		e.Kind = KindNotFound
	case status == http.StatusTooManyRequests:
		e.Kind = KindRateLimited
	case status >= 500:
		e.Kind = KindUnavailable
	case status >= 300 && status < 400:
		e.Kind = KindRedirect
	default:
		e.Kind = KindBadRequest
	}
	return e
}
