# Lantern

A small [Model Context Protocol](https://modelcontextprotocol.io) server that turns an existing [SearXNG](https://github.com/searxng/searxng) instance into a web search tool for LLMs.

Lantern translates MCP tool calls into `GET /search?format=json` requests and returns compact, de-duplicated results that a model can read and cite. Use it to give any MCP-capable client (Claude Code, Open WebUI, LibreChat, your own AI gateway, …) private web search through your own SearXNG.

```
MCP client ──(Authorization: Bearer <key>, optional)──▶ Lantern :8080/mcp
Lantern    ──(X-API-Key: <key>, optional)─────────────▶ SearXNG /search?format=json
```

- Two read-only tools, `web_search` and `news_search` (annotated with `readOnlyHint`)
- Text output for the model plus `structuredContent` with a declared output schema
- Optional API keys for the MCP endpoint, optional API key header towards SearXNG (for instances behind a reverse proxy)
- Uses nothing but `/search?format=json`, so it works with a proxy that exposes only that
- Streamable HTTP, stateless
- Hardened container: distroless, non-root, read-only filesystem, no capabilities, ~11 MB image

## Tools

| Tool | Parameters | Notes |
| --- | --- | --- |
| `web_search` | `query` (required, 1–500 chars), `categories`, `language`, `time_range`, `page`, `max_results`, `safesearch` | `categories` is an enum of `ALLOWED_CATEGORIES`; defaults to the first one |
| `news_search` | same as `web_search` without `categories` | Searches the `news` category. Only registered if `news` is an allowed category |

Parameters: `language` is a code such as `en`, `de-DE` or `auto`; `time_range` is `day`, `week`, `month` or `year`; `page` starts at 1; `max_results` is capped at `MAX_RESULTS`; `safesearch` is 0 (off), 1 (moderate) or 2 (strict).

About `time_range`: SearXNG skips every engine that does not support the time filter while it is set. In the `news` category that is often all but one engine, which can leave no results at all. Neither tool sets a time range by default, and the tool descriptions tell the model to use it only when a period is actually required.

Example text output:

```
Search results for "golang context" (page 1):

Direct answers:
- Go is a statically typed, compiled programming language. (https://go.dev)

1. context package - context - Go Packages
   https://pkg.go.dev/context
   Package context defines the Context type, which carries deadlines, …  [Source: duckduckgo, brave, startpage]
2. Go Concurrency Patterns: Context
   https://go.dev/blog/context
   In Go servers, each incoming request is handled in its own goroutine. …  [Source: brave · 2014-07-29]

Related searches: golang context timeout; golang context withcancel
```

The same data is returned as `structuredContent`:

```json
{
  "query": "golang context",
  "page": 1,
  "answers": [{ "text": "Go is a statically typed, compiled programming language.", "url": "https://go.dev" }],
  "infoboxes": [],
  "results": [
    {
      "title": "context package - context - Go Packages",
      "url": "https://pkg.go.dev/context",
      "snippet": "Package context defines the Context type, …",
      "engines": ["duckduckgo", "brave", "startpage"]
    }
  ],
  "suggestions": ["golang context timeout", "golang context withcancel"],
  "estimated_total": 1250000
}
```

Behaviour:

- Results are de-duplicated by normalised URL (scheme, `www.`, default ports, fragments, trailing slashes, `utm_*` parameters and parameter order are ignored). The engines of dropped duplicates are merged into the kept result.
- Snippets are whitespace-normalised and shortened to `SNIPPET_MAX_CHARS`, preferably at a word boundary.
- Direct answers come first, then infoboxes (title, short text, URL), results and related searches.
- No hits produce a clear "No results found" message instead of an empty result.
- Upstream problems become tool results with `isError: true` and a message the model can act on, never protocol errors:

| SearXNG response | Message to the model | Log level |
| --- | --- | --- |
| 401 / 403 | Search service rejected the request (API key invalid or missing, or JSON output disabled) | error |
| 404 | Search endpoint not found (check the URL and that `format=json` is allowed) | error |
| 3xx | Redirect (check the URL, e.g. http vs. https). Redirects are never followed, so the API key cannot leak to another host | error |
| 429 | Search service is busy (rate limit), try again later | warn |
| 5xx | Search service temporarily unavailable | warn |
| Timeout / network | Search service unreachable | warn |
| Invalid JSON | Unexpected response from the search service | error |

There are no automatic retries.

## Requirements

SearXNG must have the JSON output format enabled in `settings.yml`, otherwise it answers with 403:

```yaml
search:
  formats:
    - html
    - json
```

## Quick start

Lantern is built locally from source with Docker Compose. The compose files cover two setups. Pick one in `.env` via `COMPOSE_FILE`:

| Setup | `COMPOSE_FILE` | `SEARXNG_URL` |
| --- | --- | --- |
| SearXNG runs in Docker Compose on the same host (default) | `compose.yml:compose.searxng.yml` | `http://searxng:8080` (service name and internal port), plus `SEARXNG_NETWORK` |
| SearXNG is remote, behind a reverse proxy with an API key | `compose.yml:compose.remote.yml` | `https://search.example.com` |

```bash
git clone https://github.com/aks-jp/lantern.git
cd lantern
cp .env.example .env                       # choose the setup, adjust SEARXNG_URL
docker network ls                          # same-host setup: find the SearXNG network for SEARXNG_NETWORK

openssl rand -hex 32 > secrets/mcp_keys.txt   # one key per line
chmod 644 secrets/mcp_keys.txt                # readable for the container user (UID 65532)
# remote setup only: put the SearXNG key into secrets/searxng_api_key.txt (same permissions)

docker compose up -d --build
docker compose ps                          # wait for "healthy"
curl -s http://127.0.0.1:8090/healthz
```

On the same host, a container in the default bridge network cannot reach a SearXNG that is published on `127.0.0.1` only, not even via `host.docker.internal`. Joining the SearXNG compose network is the reliable way.

Call a tool directly:

```bash
curl -s http://127.0.0.1:8090/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H "Authorization: Bearer $(head -n1 secrets/mcp_keys.txt)" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"model context protocol","max_results":3}}}'
```

Or inspect it interactively with the [MCP Inspector](https://github.com/modelcontextprotocol/inspector):

```bash
npx @modelcontextprotocol/inspector   # Streamable HTTP, http://127.0.0.1:8090/mcp, header Authorization: Bearer <key>
```

## Configuration

Lantern is configured via environment variables. The compose files pass the ones listed in `.env.example`. Invalid values stop the server at startup with a list of all problems.

| Variable | Default | Description |
| --- | --- | --- |
| `SEARXNG_URL` | – (required) | Base URL of SearXNG; a path prefix such as `/searx` is kept |
| `SEARXNG_API_KEY` | empty | If set, sent with every request in `SEARXNG_API_KEY_HEADER` |
| `SEARXNG_API_KEY_FILE` | empty | Read the key from a file (Docker secret); takes precedence over `SEARXNG_API_KEY` |
| `SEARXNG_API_KEY_HEADER` | `X-API-Key` | Header name expected by your reverse proxy |
| `SEARXNG_TIMEOUT` | `10s` | Timeout per upstream request |
| `SEARXNG_INSECURE_SKIP_VERIFY` | `false` | Skip TLS verification (testing only; logs a warning) |
| `MCP_API_KEYS` | empty | Comma-separated allowed keys. **Empty means the endpoint is open** (logs a warning) |
| `MCP_API_KEYS_FILE` | empty | One key per line, `#` comments allowed; merged with `MCP_API_KEYS` |
| `MCP_LISTEN` | `:8080` | Listen address inside the container |
| `MCP_PATH` | `/mcp` | Path of the MCP endpoint |
| `DEFAULT_LANGUAGE` | `auto` | Search language if the call does not specify one (`.env.example` uses `de-DE`) |
| `DEFAULT_SAFESEARCH` | `1` | 0, 1 or 2 |
| `MAX_RESULTS` | `10` | Upper limit of results per call (1–50) |
| `SNIPPET_MAX_CHARS` | `300` | Maximum snippet length (50–5000) |
| `ALLOWED_CATEGORIES` | `general,news,science,it` | Categories offered to the model; the first is the default |
| `LOG_LEVEL` | `info` | `debug` also logs search queries, `info` does not |
| `LOG_FORMAT` | `json` | `json` or `text` |

Compose-only variables: `COMPOSE_FILE`, `SEARXNG_NETWORK`, `LANTERN_PORT` (host port, default `8090`), `LANTERN_VERSION` (image tag).

## Authentication

With keys configured, every request to the MCP endpoint needs one of:

```
Authorization: Bearer <key>
X-API-Key: <key>
```

Missing or wrong keys get `401` with `WWW-Authenticate: Bearer realm="lantern"`. Keys are compared in constant time. Logs never contain keys, only a key ID (the first 8 hex characters of the key's SHA-256), so you can tell clients apart:

```bash
printf %s "<key>" | sha256sum | cut -c1-8
```

To add or revoke a key, edit `secrets/mcp_keys.txt` and run `docker compose restart lantern`. `/healthz` never requires a key.

Static keys work with clients that can send a custom header. Connectors that require OAuth (such as custom connectors in claude.ai) are not supported yet. The code has an `Authenticator` interface to add that later.

## Connecting MCP clients

The compose file publishes Lantern on `127.0.0.1:8090` only, for clients on the same host.

**Claude Code:**

```bash
claude mcp add --transport http lantern http://127.0.0.1:8090/mcp \
  --header "Authorization: Bearer <key>"
```

**Open WebUI / LibreChat / AI gateways:** add an MCP server of type *Streamable HTTP* with the URL `http://127.0.0.1:8090/mcp` and the header `Authorization: Bearer <key>`. If the client runs in Docker, attach it to a shared network and use `http://lantern:8080/mcp`.

Tip: a system prompt line such as *"Use web_search for current or uncertain facts and cite the URLs."* helps smaller models use the tool.

### Remote clients

To reach Lantern from other machines, put a TLS-terminating reverse proxy in front and keep the port bound to localhost. [`deploy/nginx.example.conf`](deploy/nginx.example.conf) forwards only the MCP endpoint and `/healthz`, passes `Authorization`/`X-API-Key` through and disables buffering. Always configure clients with the `https://` URL, because many HTTP clients drop the `Authorization` header when they follow a redirect.

## Rate limits

Lantern has no cache and no rate limit of its own; SearXNG and your proxy handle that. If SearXNG sits behind a proxy that limits per API key, **all** MCP users of one Lantern instance share the limit of Lantern's single key. Give Lantern its own key with a limit that fits the number of users, or connect it to SearXNG through the internal Docker network.

## Operations

- `GET /healthz` returns `200 {"status":"ok"}` while the process runs. It does not check SearXNG, so a SearXNG outage does not restart the container.
- `lantern healthcheck` queries `/healthz` locally and sets the exit code (used by the image's `HEALTHCHECK`, since distroless has no `curl`). `lantern version` prints the version.
- `SIGTERM` triggers a graceful shutdown with a 10 s grace period.
- Every tool call is logged with tool name, duration, upstream status, number of hits and key ID. Search queries appear only at `LOG_LEVEL=debug`.

Update:

```bash
git pull && docker compose up -d --build
```

## Security

- Port bound to `127.0.0.1` only; optional API keys on the MCP endpoint
- Runs as non-root (distroless `nonroot`) with a read-only root filesystem
- All Linux capabilities dropped, `no-new-privileges`
- Limited to 64 MB RAM and 0.5 CPU; log rotation 3 × 10 MB
- No secrets in logs; search queries only at debug level
- Upstream redirects are not followed, so the SearXNG key cannot be sent to another host
- No page fetching: Lantern only returns what SearXNG returns, so it cannot be used to reach internal URLs

## Development

Requires Go 1.26.

```bash
make test     # go test -race ./...
make lint     # golangci-lint v2
make build    # bin/lantern
make docker   # local image
```

Layout: `cmd/lantern` (entry point and subcommands), `internal/config` (environment and secret files), `internal/auth` (authenticators and middleware), `internal/searxng` (client and error types), `internal/tools` (tools, formatting, schemas), `internal/server` (routing and shutdown). Tests run against an `httptest` SearXNG with fixtures from `testdata/`, and an end-to-end test drives the tools through a real MCP client.

## License

[MIT](LICENSE)
