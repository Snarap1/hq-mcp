# Repository Guidelines

## Project Overview

`hq-mcp` is a Go MCP server for read-only database access, replacing an earlier Python implementation.
It supports adapters: `postgres`, `mysql`, `mssql`, `odbc` (compat), `clickhouse`, `redis`.
It reads its own TOML config files (`hq-mcp.toml`, `.hq-mcp.toml`, `$XDG_CONFIG_HOME/hq-mcp/config.toml`, `[tool.hq-mcp]` in `pyproject.toml`); no other tool's config files are read.
**`docs/plans/PLAN.md` is the authoritative spec** — the project is mid-build; this document describes both implemented and planned behavior, and the plan is the source of truth for anything not yet written.

## Architecture & Data Flow

One domain package per concern under `internal/`; `main.go` at the root is only the stdio entrypoint.
`internal/config` (discovery, merge, profile validation, option accessors), `internal/adapters` (`DB`, openers, ODBC/URL `conn_str`), `internal/readonly` (SQL screening), `internal/values` (JSON-safe conversion), `internal/sqlrows` (scan buffer, streaming), `internal/query` (`run_query`), `internal/export` (`export_query`), `internal/catalog` (`get_schema`/`get_columns`), `internal/rediscmd` (`run_redis`), `internal/server` (MCP tools).
Dependency direction: `config` ← `adapters` ← {`catalog`, `rediscmd`, `server`}, `readonly`/`values`/`sqlrows` ← {`query`, `export`, `rediscmd`}; nothing imports `server` except `main`.

Data flow per tool call:

1. Load config via hq-mcp discovery (`config.Load`) → pick profile (explicit name, else `default_profile`).
2. Open a **fresh connection per request, close after** (`adapters.Open`; parity with the Python server; lets the server run alongside a live database TUI session). No pooling across calls.
3. Screen the statement: SQL via `readonly.Violation`; Redis via the read-only command whitelist in `rediscmd` (default-deny).
4. Execute, convert driver values to JSON-safe values (`values.JSON`), return typed Out struct.

Key patterns:
- Adapter dispatch from raw `map[string]any` profile tables — no profile structs; per-adapter key validation rejects unknown keys with the valid-key list.
- Config merge is **top-level key replacement**, not deep merge (a higher-priority file's `profiles` table replaces the whole map), matching the upstream config merge semantics.
- `pyproject.toml` contributes only its `[tool.hq-mcp]` section; top-level `keymaps` is rejected like any other unknown key.
- SQL screening is defense-in-depth: read-only prefix check + stacked-statement rejection + write-keyword blacklist over comment/string-stripped SQL.
- INSERT/UPDATE are **intentionally allowed**; `do` is a write keyword, so `INSERT ... ON CONFLICT DO UPDATE` is refused — do not "fix" either.

## Key Directories

- `./` — `main.go` (entrypoint) and `e2e_test.go` (gated e2e suite).
- `internal/` — one package per domain, each with its own `_test.go`.
- `docs/plans/` — `PLAN.md`, the full spec with execution progress log and resume instructions.

## Development Commands

```sh
go build -o hq-mcp .        # build binary (stdout is the MCP channel; diagnostics go to stderr)
go build ./...              # compile check
go test ./...               # unit tests (no network)
HQ_MCP_E2E=1 go test -run TestE2E -v .   # e2e; requires built binary, running DBs, docker redis on 127.0.0.1:6399
go vet ./... && gofmt -l .  # lint/format check
```

MCP registration (user-run): `claude mcp add hq-mcp -- /path/to/hq-mcp/hq-mcp`, or a `.claude/settings.json` snippet (planned README).

Config the binary consumes: hq-mcp TOML candidates in priority order — `$HOME/pyproject.toml`, `$HOME/.hq-mcp.toml`, `$HOME/hq-mcp.toml`, `$XDG_CONFIG_HOME/hq-mcp/config.toml` (+ `.hq-mcp.toml`, `hq-mcp.toml`), then cwd `pyproject.toml`/`.hq-mcp.toml`/`hq-mcp.toml`; env `HQ_MCP_CONFIG` appends last (highest priority).

## Code Conventions & Common Patterns

- Standard `gofmt` formatting; stdlib-first (`net/url`, `database/sql`, `encoding/csv`, `encoding/json`).
- Errors: plain `fmt.Errorf`/`errors.New` with lowercase messages; message wording is **parity-pinned to the Python server** (e.g. `Refused (read-only server): <reason>`) — check `docs/plans/PLAN.md` before rewording.
- Handlers return Go `error` (SDK sets `IsError`), never error-in-payload dicts.
- Profile option accessors: `config.OptStr`/`OptInt`/`OptBool`/`OptConnStr`/`DBOpt` reading from `map[string]any` with typed errors.
- `conn_str` accepts URL form (`postgres://`, `mysql://`, `sqlserver://`, `http(s)://`, `clickhouse://`) — parsed with `net/url`; explicit profile keys take precedence over URL parts.
- ODBC `conn_str` may be a string **or a list of strings** (joined with a single space) — real profiles use the list form.
- Driver values convert recursively: `[]byte` → string (UTF-8) else base64 with `b64:` prefix, `time.Time` → RFC3339, fallback `fmt.Sprintf("%v")`.
- English only for tool messages and docs.
- Repo rule (from the Python repo): each sentence on its own line in prose docs (README).

## Important Files

- `main.go` — stdio entrypoint only: stderr logging, `server.Serve(ctx)`.
- `internal/config` — config discovery, merge, per-adapter key validation, URL/ODBC conn_str handling. Done.
- `internal/adapters` — `DB` interface, `SQL`/`Redis` connection types, openers, ODBC parser, ClickHouse HTTP-on-8123 default. Done.
- `internal/readonly` — `stripSQL` + `Violation` SQL screening. Done, parity-locked; port verbatim, do not "fix".
- `internal/server` — the six tools (`list_profiles`, `get_schema`, `get_columns`, `run_query`, `run_redis`, `export_query`) and their In/Out structs. Done.
- `go.mod` — module `hq-mcp`, `go 1.25.0`; deps: `modelcontextprotocol/go-sdk v1.8.0`, `pelletier/go-toml/v2`, `jackc/pgx/v5`, `go-sql-driver/mysql`, `microsoft/go-mssqldb`, `ClickHouse/clickhouse-go/v2`, `redis/go-redis/v9`.
- `docs/plans/PLAN.md` — spec, verification checklist, execution progress log (update it when completing steps).

## Runtime/Tooling Preferences

- Go toolchain 1.25 (`GOTOOLCHAIN=auto`; local go1.25.0 installed). `dl.google.com` / `proxy.golang.org` reachable.
- Server speaks MCP over **stdio — stdout is the protocol channel**; all diagnostics go to stderr (`log.SetOutput(os.Stderr)`). Never `fmt.Println` to stdout.
- Docker + `redis-cli` available for the redis e2e fixture (`redis:7-alpine` on `127.0.0.1:6399`).
- ClickHouse: use HTTP interface (port 8123/8443) — native port 9000 does not work in this environment; protocol defaults to `http` when port is 8123/8443.

## Testing & QA

- Unit tests live next to the package they cover (`internal/<pkg>/<pkg>_test.go`); the e2e suite is the only test in the root `main` package.
- Unit tests: behavioral, table-driven, no network — assert screening decisions (`SELECT 1` allowed, `DELETE FROM t` refused, strings/comments stripped), ODBC/URL parsing against the real `ms-alpha`/`ch-alpha` profile shapes, value conversion.
- E2E gated behind `HQ_MCP_E2E=1` (skip otherwise): spawns the built binary via `mcp.Client` + `CommandTransport`, cwd = temp dir with an `hq-mcp.toml` (from `docs/plans/dev.hq-mcp.toml`); asserts against real `pg-eps`, `ch-alpha`, and the docker redis fixture.
- Every behavior change to screening/refusal messages needs a matching table-driven case; refusal message wording is part of the contract.
