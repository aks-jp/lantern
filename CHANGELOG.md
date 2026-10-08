# Changelog

All notable changes to this project are documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/).

## [0.1.1] - 2026-10-08

### Changed

- `news_search` no longer defaults to `time_range=week`. SearXNG skips engines without time filter support while a time range is set, which left only one or no news engine and often no results. The `time_range` description now warns about this.

## [0.1.0] - 2026-10-08

### Added

- MCP server over Streamable HTTP (stateless) that forwards searches to SearXNG via `GET /search?format=json`.
- Tools `web_search` and `news_search` with text output, `structuredContent` and a declared output schema; results are de-duplicated by normalised URL and snippets are shortened.
- Optional API key authentication of the MCP endpoint (`Authorization: Bearer` or `X-API-Key`), constant-time comparison, keys from environment and/or file.
- Optional API key header towards SearXNG with configurable header name; redirects are not followed.
- Upstream failures are reported as tool errors with actionable messages.
- `/healthz`, `lantern healthcheck` and `lantern version`; graceful shutdown; structured logging without secrets or (at info level) search queries.
- Distroless container image, Docker Compose setups for a local or remote SearXNG, nginx example and CI.
