# hq-mcp: Go MCP server for database access

## Context

Replace the existing Python MCP server with a Go binary built in this repo.
The Python server depends on third-party DB adapter packages and has no Redis support.
The Go version keeps the same conventional TOML config format and file discovery, and supports these adapters: `postgres`, `mysql`, `mssql`, `odbc` (compat), `clickhouse`, `redis`.
Development and verification happen **exclusively against Docker containers bound to `127.0.0.1`**; no real database hosts, no real credentials, no edits to any other tool's config.
Key facts (verified on this workstation):
- Local Go is 1.22.2 at `$HOME/sdk/go` (go1.25.0); the official MCP Go SDK requires Go 1.25. `dl.google.com` and `proxy.golang.org` are reachable, so toolchain auto-download works.
- Docker and `redis-cli` are available; all DB fixtures are containers (see "Local Docker fixtures").

## Approach

Single Go module `hq-mcp`; one domain package per concern under `internal/`, with `main.go` at the root as a thin entrypoint and `e2e_test.go` next to it.
Layout: `main.go`, `internal/config` (discovery, merge, profile validation, option accessors), `internal/adapters` (`DB` interface, per-request openers, ODBC/URL `conn_str`), `internal/readonly` (SQL screening), `internal/values` (JSON-safe value conversion), `internal/sqlrows` (row scan buffer and streaming), `internal/query` (`run_query`), `internal/export` (`export_query`), `internal/catalog` (`get_schema`/`get_columns`), `internal/rediscmd` (`run_redis`), `internal/server` (MCP tool registration). Each package owns its `_test.go`; only the e2e suite lives in the root package. `README.md`.

### 1. Module scaffold

- `go mod init hq-mcp` with `go 1.25.0` in go.mod. Set `GOTOOLCHAIN=auto` (default) so the first build auto-downloads go1.25 from dl.google.com.
- Dependencies: `github.com/modelcontextprotocol/go-sdk/mcp` (official SDK, latest v1.7+), `github.com/pelletier/go-toml/v2`, `github.com/jackc/pgx/v5/stdlib`, `github.com/go-sql-driver/mysql`, `github.com/microsoft/go-mssqldb`, `github.com/ClickHouse/clickhouse-go/v2`, `github.com/redis/go-redis/v9`.
- Verify with `go build ./...` before anything else.

### 2. Config loading (`config.go`)

hq-mcp has its own config files, laid out the conventional way (home, then XDG config dir, then cwd; the same order the upstream reference implementation uses):
- Candidate files in priority order (later overrides earlier via top-level key replacement, i.e. a `profiles` table from a higher-priority file replaces the whole `profiles` map, not deep-merges):
  1. `$HOME/pyproject.toml`, `$HOME/.hq-mcp.toml`, `$HOME/hq-mcp.toml`
  2. `$XDG_CONFIG_HOME/hq-mcp/config.toml`, `.hq-mcp.toml`, `hq-mcp.toml`
  3. cwd: `pyproject.toml`, `.hq-mcp.toml`, `hq-mcp.toml`
  4. If env `HQ_MCP_CONFIG` is set, that file is appended last (highest priority).
- TOML parse via `go-toml/v2` into `map[string]any`, then to `Config{DefaultProfile string; Profiles map[string]map[string]any}`.
- `pyproject.toml` contributes only its `[tool.hq-mcp]` section.
- Accept only `default_profile` and `profiles` at top level, error on anything else, a `keymaps` table included.
- Helper accessors: `optStr`/`optInt`/`optBool`/`optConnStr` reading from the profile map with per-adapter key validation (unknown key -> error listing valid keys, mirroring how the upstream adapters reject unknown options).
- Each adapter accepts these keys (aliases in parentheses):
  - `postgres`: `adapter`, `host`, `port`, `user`, `password`, `dbname` (`database` alias), `conn_str` (URL form), `sslmode` (default `prefer`)
  - `mysql`: `adapter`, `host`, `port` (default 3306), `user`, `password`, `dbname` (`database` alias), `conn_str` (URL form), `tls` (default `false`)
  - `mssql`: `adapter`, `host`, `port` (default 1433), `user`, `password`, `dbname` (`database` alias), `conn_str` (sqlserver URL or ODBC string), `encrypt` (default `disable`)
  - `odbc`: `adapter`, `conn_str` (ODBC semicolon string, string or list of strings; parsed, see step 3)
  - `clickhouse`: `adapter`, `host`, `port`, `user`, `password`, `database`, `protocol` (`http` or `native`), `secure` (bool, TLS), `conn_str` (URL form)
  - `redis`: `adapter`, `host` (default `localhost`), `port` (default 6379), `user`, `password`, `database` (int 0-15), `secure` (bool, TLS), `separator` (string, default `:`)
- `adapter` value `duckdb`/`sqlite`/anything else -> error: `adapter %q is not supported; supported: postgres, mysql, mssql, odbc, clickhouse, redis`.
- If `conn_str` is a URL (`postgres://`, `mysql://`, `sqlserver://`, `http(s)://`, `clickhouse://`), parse with `net/url` and fill host/port/user/password/dbname; explicit keys take precedence over URL parts.

### 3. Connection layer (`adapters.go`)

- Interface (no equivalent exists in the repo; new code):
  ```go
  type DB interface {
      Kind() string   // "postgres" | "mysql" | "mssql" | "clickhouse" | "redis"
      Close()
  }
  ```
  SQL DBs additionally expose `*sql.DB`; redis exposes `*redis.Client`. Implement as concrete per-adapter openers in this file, dispatched from profile map.
- Open per request and close after (parity with the Python server, which opens a fresh connection per tool call so it works alongside a live database TUI session). No pooling across calls.
- DSNs / openers, exact:
  - postgres: `sql.Open("pgx", "host=H port=P user=U password=W dbname=D sslmode=S")` (import `github.com/jackc/pgx/v5/stdlib` for driver registration; default sslmode `prefer`).
  - mysql: `sql.Open("mysql", "U:W@tcp(H:P)/D?parseTime=true&tls=T")` (import `_ "github.com/go-sql-driver/mysql"`; default tls `false`).
  - mssql: `sql.Open("sqlserver", "sqlserver://U:W@H:P?database=D&encrypt=E")` (import `github.com/microsoft/go-mssqldb`; default encrypt `disable`).
  - odbc: parse the ODBC `conn_str` (split on `;`, respect `{...}` braced values like `{ODBC Driver 18 for SQL Server}`), extract `Server` (may be `host,port`), `Database`, `Uid`, `Pwd`, `Encrypt`, then open via the mssql path. Encrypt mapping: `no|false|0|optional` -> `disable`; `strict` -> `strict`; anything else (`yes|true|1|mandatory`) -> `true`. Real-world profiles use the ODBC string/list form; keep them working unchanged.
  - clickhouse: `clickhouse.OpenDB(&clickhouse.Options{Addr: []string{"H:P"}, Auth: clickhouse.Auth{Database, Username, Password}, Protocol: clickhouse.HTTP | clickhouse.Native, TLS: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host} if secure})`. Protocol default: `http` if `port` is 8123 or 8443, else `native`. Port default: 8123 when no port given.
  - redis: `redis.NewClient(&redis.Options{Addr: "H:P", Username, Password, DB, TLS: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host} if secure})`.

### 4. Read-only SQL screening (`readonly.go`)

Port the Python server's screening logic verbatim (its `stripSQL` + `readOnlyViolation`; this is the user's own modified logic - do not "fix" it):
- `stripSQL(sql)`: regex-replace, in order, with a single space:
  1. `/\*.*?\*/` with `(?s)` flag
  2. `--[^\n]*`
  3. `\$\$.*?\$\$` with `(?s)`
  4. `'(?:[^']|'')*'`
  5. `"(?:[^"]|"")*"`
- `readOnlyViolation(sql) string` (empty means allowed):
  1. Split cleaned SQL on `;`; if more than one non-blank statement -> `multiple statements are not allowed; send one read-only query`.
  2. Empty -> `empty statement`.
  3. Strip leading `(` and lowercase; if it does not start with one of `select, with, explain, show, describe, desc, table, insert, update` -> refuse. (INSERT and UPDATE are intentionally allowed - parity with the user's current server.)
  4. Extract words matching `[a-z_]+` from the body; if any is in the write-keyword set -> refuse with the sorted list. Write keywords, exact set: `delete, drop, truncate, alter, create, replace, merge, upsert, grant, revoke, vacuum, attach, detach, copy, call, do, lock, rename, reindex, refresh, comment, nextval, setval`.
- Known accepted parity limitation: `INSERT ... ON CONFLICT DO UPDATE` is refused because `do` is a write keyword. Do not change this.

### 5. Redis screening and execution (`redis.go`)

- Read-only command whitelist (lowercase; command = first word; default-deny anything not listed):
  `ping, time, info, dbsize, scan, keys, type, exists, ttl, pttl, strlen, get, getrange, getbit, mget, bitcount, lpos, hget, hgetall, hkeys, hlen, hmget, hvals, hscan, lrange, lindex, llen, scard, smembers, sismember, smismember, srandmember, sscan, zcard, zcount, zlexcount, zrange, zrangebyscore, zrangebylex, zrevrange, zrank, zrevrank, zscore, zmscore, zscan, xlen, xrange, xrevrange, xinfo, object, memory, randomkey, geopos, geodist, geohash, geosearch`
- Excluded on purpose: all writes (`set`, `del`, `expire`, ...), blocking commands (`blpop`, `brpop`, ...), `eval`/`evalsha` (arbitrary writes via Lua), `subscribe`/`monitor` (break stdio protocol), `config`, `select`, `auth`, `flush*`, `shutdown`.
- Refusal message: `command %q is not allowed; this server permits read-only Redis commands only`.
- Execution: `client.Do(ctx, append([]any{command}, stringArgs...)...)` then `.Result()`; convert result recursively to JSON-safe values: `[]byte` -> string, `time.Time` -> RFC3339, `int64`/`string`/`nil` as-is, `[]any`/`map[string]any` recursively, anything else -> `fmt.Sprintf("%v")`.

### 6. Catalog: `get_schema` / `get_columns` (`catalog.go`)

`get_schema(profile, path, name_filter, include_columns)` returns the flat list of children at the level addressed by `path`. Node: `{name string, kind string, count int}` where kind is one of `database, schema, table, view, namespace, key`. Response: `{nodes: [...], columns: [{name, type, nullable}] (only when include_columns at table level), truncated bool}`. If the marshaled JSON exceeds 60,000 chars, return error `result too large; drill down using path` (parity with the Python server's cap).

Queries per adapter (placeholders: `?`; works on all four drivers):
- postgres: `[]` -> `SELECT current_database()` (single database node; PG cannot cross databases from one connection). `[x]` -> `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema') AND schema_name NOT LIKE 'pg_toast%' ORDER BY schema_name`. `[x, y]` -> shared tables query (below).
- mysql: `[]` -> `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','information_schema','performance_schema','sys') ORDER BY schema_name`. `[x]` -> shared tables query.
- mssql: `[]` -> `SELECT DB_NAME()` (single node; mssql queries one database per connection). `[x]` -> `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('sys','INFORMATION_SCHEMA','guest') ORDER BY schema_name`. `[x, y]` -> shared tables query.
- shared tables query (postgres/mysql/mssql): `SELECT table_name, table_type FROM information_schema.tables WHERE table_schema = ? AND lower(table_name) LIKE lower(?) ORDER BY table_name` with name_filter as `%filter%` (empty filter -> `%%`). kind = `view` when `table_type` contains `VIEW`, else `table`.
- clickhouse: `[]` -> `SELECT name FROM system.databases ORDER BY name`. `[x]` -> `SELECT name, engine FROM system.tables WHERE database = ? AND positionCaseInsensitive(name, ?) > 0 ORDER BY name` (name_filter passed raw, empty -> `''`; `positionCaseInsensitive` exists in CH). kind = `table`.
- redis: pattern = `strings.Join(path, sep) + sep + "*"` (or `*` at root), `sep` = profile `separator` (default `:`). Iterate `SCAN` with `COUNT 500` until cursor 0 or 10,000 keys seen. Group keys by the next segment after the path prefix; nodes: `namespace` (with key count) for segments, `key` for keys with no further segment. When `include_columns` is true and the level has `key` nodes, sample `TYPE` for at most 25 of them and include the type directly in each node as `{name, kind: "key", count: 0, type: <sampled>}`. Cap: at most 500 nodes + `truncated: true` if more.
- `get_columns(profile, path)`: takes the trailing 2 path segments: `(schema, table)` for postgres/mssql, `(database, table)` for mysql/clickhouse. Error if fewer than 2 segments, with per-adapter usage example.
  - postgres/mysql/mssql: `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`.
  - clickhouse: `SELECT name, type FROM system.columns WHERE database = ? AND table = ? ORDER BY position`; `nullable` = `"YES"` if the type contains `Nullable(`, else `"NO"`.
  - redis profile -> error `get_columns is not available for redis; use get_schema`.
  - Response: `{table: "<joined path>", columns: [{name, type, nullable}]}`.

### 7. `run_query` (`query.go`)

- Only SQL adapters; redis profile -> error `run_query is not available for redis; use run_redis`.
- Screen with `readOnlyViolation` first; refusal -> error `Refused (read-only server): <reason>` (parity message).
- Execute via `sql.DB.Query`, read columns from `rows.Columns()`, stop reading after `limit` rows (default 50; `limit=0` means unlimited). Do not rewrite/inject SQL syntax.
- Value conversion (same recursive rules as redis): driver values -> `nil`, `bool`, `int64`, `float64`, `string`, `[]byte` -> string if valid UTF-8 printable else base64 (prefix `b64:`), `time.Time` -> RFC3339, fallback `fmt.Sprintf("%v")`.
- Response: `{columns: [...], rows: [[...]], row_count: n, truncated: bool}` (`truncated` = more rows existed when limit hit).

### 8. `export_query` (`export.go`)

- SQL adapters only; same screening as `run_query`.
- Args: `profile`, `sql`, `dest_path`, `format` (one of `csv`, `json`, `ndjson`; default inferred from extension: `.csv`/`.tsv` -> csv, `.json` -> json, `.ndjson` -> ndjson; error `cannot infer format from extension %q; pass format explicitly` otherwise), `limit` (default 0 = unlimited).
- Parent directory must exist; error `parent directory does not exist: <dir>` otherwise. Do not create directories.
- Stream rows while writing (never materialize all rows in memory): csv via `encoding/csv` with header row; json as a JSON array of row objects streamed with `json.Encoder`; ndjson as one object per line. All values stringified for csv; json keeps converted types.
- Response: `{path, format, row_count}`.

### 9. MCP server and tools (`main.go`)

- `server := mcp.NewServer(&mcp.Implementation{Name: "hq-mcp", Version: "0.1.0"}, nil)`; tools via `mcp.AddTool(server, &mcp.Tool{Name, Description}, handler)` with typed In/Out structs (jsonschema tags in doc comments, as in the SDK README example); run with `server.Run(ctx, &mcp.StdioTransport{})`.
- CRITICAL: stdout is the MCP protocol channel. All diagnostics go to stderr (`log.SetOutput(os.Stderr)` in main). Never `fmt.Println` to stdout.
- Tool list, exact signatures (In struct fields; all optional fields have defaults):
  1. `list_profiles()` -> `{profiles: [{name, adapter, is_default}]}`. Never returns passwords.
  2. `get_schema(profile?, path []string, name_filter?, include_columns bool)` -> as step 6.
  3. `get_columns(profile, path []string)` -> as step 6.
  4. `run_query(profile?, sql string, limit int = 50)` -> as step 7.
  5. `run_redis(profile?, command string, args []string)` -> `{command, result}` as step 5.
  6. `export_query(profile?, sql string, dest_path string, format?, limit int = 0)` -> as step 8.
- Empty `profile` -> use `default_profile`; error `no profile specified and no default_profile is set` when neither exists. Unknown profile -> error `unknown profile %q; run list_profiles`.
- Tool descriptions: port the English docstrings from the Python server (e.g. run_query's explanation of read-only screening, get_schema's path drill-down examples).
- Errors: return Go `error` from handlers (SDK sets `IsError` and surfaces the message) - not error-in-payload dicts.

### 10. Unit tests

Behavioral, table-driven, no network:
- `readonly_test.go`: `SELECT 1` allowed; `DELETE FROM t` refused (prefix); `UPDATE t SET a=1` allowed; `INSERT INTO t VALUES (1)` allowed; `CREATE TABLE x (a int)` refused; `WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x` refused (keyword `delete`); `SELECT 1; DROP TABLE t` refused (multiple); `SELECT 'this string contains delete' FROM t` allowed (string stripped); `SELECT 1 -- delete` allowed (comment stripped); `SELECT $$ drop $$ FROM t` allowed (dollar-quote stripped); `EXPLAIN SELECT * FROM t` allowed; `` (empty) refused.
- `config_test.go`: ODBC parse of a representative string in the real profile shape but with loopback values (`Driver={ODBC Driver 18 for SQL Server};Server=127.0.0.1,1433;Database=dev_db;Uid=sa;Pwd=pw;Encrypt=no;`) -> host `127.0.0.1`, port `1433`, db `dev_db`, encrypt `disable`; ODBC list form joined with a single space; URL conn_str parse (`postgres://u:p@127.0.0.1:5432/d`); unknown key error for a postgres profile with `foo = 1`; adapter priority default (`http` for 8123, `native` for 9000).
- `redis_test.go`: `GET` allowed; `DEL`, `SET`, `EVAL`, `CONFIG SET`, `SUBSCRIBE` refused; result conversion (`[]byte` -> string, nested `[]any`).
- `query_test.go`: value conversion table (`[]byte{0xff, 0xfe}` -> `b64:`-prefixed string, `time.Time` -> RFC3339, `nil` -> nil).

### 11. Local Docker fixtures (dev + e2e only)

All verification runs against containers bound to `127.0.0.1`; **no real hosts, no real credentials, no edits to any other tool's config**.
Non-default host ports are used so fixtures never clash with anything already listening locally:

| profile (dev) | container image | host port -> container port | credentials |
| --- | --- | --- | --- |
| `pg-dev` | `postgres:16-alpine` | `127.0.0.1:55432` -> 5432 | `hq` / `hq`, db `hqdb` |
| `my-dev` | `mysql:8` | `127.0.0.1:53306` -> 3306 | `hq` / `hq`, db `hqdb` |
| `ms-dev` | `mcr.microsoft.com/mssql/server:2022-latest` | `127.0.0.1:51433` -> 1433 | `sa` / `Hq!Passw0rd`, db `hqdb` |
| `ch-dev` | `clickhouse/clickhouse-server:24-alpine` | `127.0.0.1:58123` -> 8123, `127.0.0.1:59000` -> 9000 | `hq` / `hq`, db `hqdb` |
| `redis-dev` | `redis:7-alpine` | `127.0.0.1:56379` -> 6379 | none |

Bring up (scripted as `docs/plans/fixtures.sh` so the e2e setup is reproducible; each `docker run` uses `--rm`-less named containers, a healthcheck wait, and a seed step):
- postgres: `CREATE TABLE public.widgets(id serial primary key, name text, price numeric); INSERT ...` plus a second schema `analytics.widgets_extra` so `get_schema` drill-down has >1 schema.
- mysql: `CREATE TABLE widgets(...)` in `hqdb`, plus a second database `hqdb2` for root-level listing.
- mssql: same table in `dbo` (`hqdb`), plus a schema `analytics`.
- clickhouse: `CREATE DATABASE hqdb; CREATE TABLE hqdb.widgets(id UInt32, name String, price Decimal(10,2)) ENGINE=MergeTree ORDER BY id;` plus a `Nullable(...)` column so `get_columns` nullability is exercised.
- redis: `SET user:1000 '{"id":1000}'`, `SADD user:1000:roles admin viewer`, `SET plainkey 1`, `LPUSH queue:a a b c`.
- Keep the dev TOML fixture in `docs/plans/dev.hq-mcp.toml` (one profile per row above, all `host = "127.0.0.1"`), so nothing real ever enters the repo.
- Teardown at the end of a verification round: `docker rm -f hq-pg-dev hq-my-dev hq-ms-dev hq-ch-dev hq-redis-dev`.

### 12. README.md (in hq-mcp)

Model on the Python server's README structure: tools table, supported adapters table, config format (same file examples, all loopback/placeholder hosts), build (`go build -o hq-mcp .`), registration (`claude mcp add hq-mcp -- /path/to/hq-mcp/hq-mcp` or a `.claude/settings.json` snippet), read-only policy, clickhouse HTTP-vs-native note, redis profile example. Each sentence on its own line (Pavel's repo rule). English.

## Critical files & anchors

- repo root - new module; all files enumerated in Approach.
- the Python server's screening module (logic to port verbatim; tool docstrings nearby) - external reference, not in this repo.
- the upstream Python config module - config discovery order and merge semantics to mirror - external reference.
- `docs/plans/fixtures.sh` + `docs/plans/dev.hq-mcp.toml` - the only config/credentials the tests use.
- `https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/README.md` - SDK API shape (AddTool handler signature, StdioTransport).

## Verification

Prerequisites: network to dl.google.com / proxy.golang.org (verified), Docker running, fixtures up (see step 11; all on `127.0.0.1`).

1. Build: `go build -o hq-mcp .` in the repo root (first run downloads the go1.25 toolchain and modules).
2. Unit: `go test ./...` - all table-driven cases from step 10.
3. Fixtures: `bash docs/plans/fixtures.sh up` (starts the five containers, waits for health, seeds data) and `bash docs/plans/fixtures.sh down` for teardown.
4. E2E (`e2e_test.go`, gated by env `HQ_MCP_E2E=1`; skipped otherwise): write `dev.hq-mcp.toml` into `t.TempDir()` as `hq-mcp.toml` (or set `HQ_MCP_CONFIG` to it), spawn the built binary via the SDK's `mcp.Client` + `CommandTransport` with cwd = that temp dir, then assert:
   - `list_profiles` -> contains `pg-dev`, `my-dev`, `ms-dev`, `ch-dev`, `redis-dev`
   - `run_query(pg-dev, "SELECT 1 AS one")` -> `rows == [[1]]`
   - `run_query(pg-dev, "DELETE FROM widgets")` -> `IsError` with refusal message
   - `run_query(ch-dev, "SELECT version()")` -> non-empty string row (proves ClickHouse end to end)
   - `run_query(ch-dev, "SELECT version()")` against a `protocol = "native"` clone of the same profile on port 59000 -> also works (native path stays covered)
   - `run_query(ms-dev, "SELECT 1 AS one")` -> works
   - `run_query(my-dev, "SELECT 1 AS one")` -> works
   - `run_query(redis-dev, ...)` -> `IsError` with `run_query is not available for redis`
   - `get_schema(pg-dev, [])` -> one node named `hqdb`
   - `get_schema(pg-dev, ["hqdb"])` -> schemas `analytics` and `public`
   - `get_schema(pg-dev, ["hqdb", "public"])` -> node `widgets` (kind `table`)
   - `get_columns(pg-dev, ["hqdb", "public", "widgets"])` -> non-empty columns incl. `price`
   - `get_columns(ch-dev, ["hqdb", "widgets"])` -> `nullable` `YES` for the Nullable column, `NO` for `id`
   - `export_query(pg-dev, "SELECT 1 AS one", <tmp>/out.csv)` -> file exists, contains header `one` and row `1`
   - `run_redis(redis-dev, "GET", ["user:1000"])` -> `{"id":1000}`
   - `run_redis(redis-dev, "DEL", ["user:1000"])` -> `IsError` refusal
   - `run_redis(redis-dev, "SCAN", ["0", "MATCH", "user:*", "COUNT", "100"])` -> cursor array containing `user:1000` and `user:1000:roles`
   - `get_schema(redis-dev, [])` -> nodes `user` (namespace) and `plainkey` (key)
   Run: `HQ_MCP_E2E=1 go test -run TestE2E -v .`
5. Manual smoke of the registration: `claude mcp add hq-mcp -- <repo>/hq-mcp`, then in a Claude session call `list_profiles` and `run_query(pg-dev, "SELECT 1")`.
6. Cleanup: `bash docs/plans/fixtures.sh down`; remove `/tmp` scratch files.

## Assumptions & contingencies

- INSERT/UPDATE remain allowed for SQL (user's choice, parity with their modified server); write-keyword blacklist ported verbatim, including the `do`-keyword refusal of `INSERT ... ON CONFLICT DO UPDATE`.
- Redis: generic `run_redis` with a default-deny allowlist (user's choice); reads plus `SET` (with `NX`/`XX`/`EX`/`PX`/`KEEPTTL`) are allowed, every other write (`DEL`, `EXPIRE`, `MSET`, `SETNX`, ...) refused. Added 2026-09-28; the refusal wording changed from `permits read-only Redis commands only` to `permits read commands and SET only`, which breaks parity with the Python server on purpose.
- Export formats: csv/json/ndjson only (user's choice); no parquet dependency.
- ClickHouse: default protocol stays `http` when the port is 8123/8443, `native` otherwise; the container exposes both, so both paths are covered by e2e. If the `clickhouse-go` HTTP path misbehaves locally, diagnose with `curl 'http://127.0.0.1:58123/?query=SELECT%201'` (with the fixture's auth) - do not silently change defaults.
- Go toolchain auto-download; if it fails, install go1.25 manually: `curl -L https://dl.google.com/go/go1.25.0.linux-amd64.tar.gz | tar -C ~/sdk` and set `PATH`/`GOROOT`, or pin an older SDK version compatible with the installed Go.
- Tool messages and README in English (parity with the current server).
- Per-request connections, no pooling across calls (parity with the Python server).
- Redis profiles living in a shared TOML config are not usable by a SQL-only TUI; accepted tradeoff for keeping one config file.
- Config merge is top-level replacement (a higher-priority file's `profiles` table replaces the whole map), matching the upstream merge semantics.
- Real-world profiles (non-loopback hosts, real credentials) are never committed or touched by the tests; e2e uses only `docs/plans/dev.hq-mcp.toml`.

## Execution progress (2026-09-28)

All steps 1-12 are DONE and verified; the plan is fully implemented.
- Step 1 (scaffold) DONE: `go mod init hq-mcp`, `go 1.25.0` in go.mod, all seven dependencies resolved (`modelcontextprotocol/go-sdk v1.8.0`, `pelletier/go-toml/v2 v2.4.3`, `jackc/pgx/v5`, `go-sql-driver/mysql`, `microsoft/go-mssqldb v1.11.2`, `ClickHouse/clickhouse-go/v2`, `redis/go-redis/v9 v9.22.0`); go1.25 toolchain auto-downloaded.
- Step 2 DONE: `config.go` (discovery, top-level-replacement merge, per-adapter key validation, URL/ODBC `conn_str` handling).
- Step 3 DONE: `adapters.go` (`DB` interface, per-request openers, `parseODBC` with `{...}` awareness and encrypt mapping).
- Step 4 DONE: `readonly.go` (parity-locked port of the Python screening logic).
- Steps 5-9 DONE: `redis.go`, `catalog.go`, `query.go`, `export.go`, `main.go` (6 tools, stdout reserved for the protocol).
- Step 10 DONE: `readonly_test.go`, `config_test.go`, `redis_test.go`, `query_test.go` (table-driven, no network).
- Step 11 DONE: `docs/plans/fixtures.sh` (up/down) and `docs/plans/dev.hq-mcp.toml`; five containers on `127.0.0.1` with throwaway credentials.
- Step 12 DONE: `README.md`.
- Verification run 2026-09-28: `gofmt -l .` and `go vet ./...` clean, `go test ./...` ok, `HQ_MCP_E2E=1 go test -run TestE2E .` ok (19 e2e tests) against the live fixtures.
- 2026-09-28: cutover to own config files — `hq-mcp.toml`/`.hq-mcp.toml`, `$XDG_CONFIG_HOME/hq-mcp/`, `[tool.hq-mcp]`; other tools' files no longer read and `keymaps` now rejected; fixture named `docs/plans/dev.hq-mcp.toml`.
- 2026-09-28: added `apm/`, an APM package shipping the `hq-mcp-config` skill (`SKILL.md`, four references, and `scripts/hq-mcp-check.py`, which drives the real binary over stdio to validate a config). Verified: `apm audit` clean, `apm install` into scratch projects for the `claude` and `copilot` targets, `apm pack` bundle reinstall, and the checker green against the live fixtures (7 profiles, 7 probes).
- 2026-09-28: the APM package now registers the server as well as teaching it — `apm/apm.yml` declares a self-defined stdio server under `dependencies.mcp` (command `hq-mcp` from `PATH`), so `apm install Snarap1/hq-mcp/apm` writes the `hq-mcp` entry into each selected harness's native MCP config and `apm uninstall` removes it. Verified: fresh-project installs for the `claude` and `opencode` targets (`.mcp.json` and `opencode.json` written, lockfile records `mcp_servers`), uninstall strips the entry, and the declared bare `hq-mcp` command was driven over stdio by the checker against `docs/plans/dev.hq-mcp.toml` (7 profiles, exit 0). `apm pack` bundles carry the skill only, so a bundle install still registers by hand.
- 2026-09-28: split the flat `main` package into domain packages under `internal/` (`config`, `adapters`, `readonly`, `values`, `sqlrows`, `query`, `export`, `catalog`, `rediscmd`, `server`); `main.go` is now only the stderr/stdio entrypoint, and every unit test sits next to the package it covers. Behavior, refusal wording, and the tool wire format are unchanged; `catalog` and `rediscmd` now reach connections through the exported `adapters.SQL`/`adapters.Redis` accessors. Also fixed a dead error check in the mssql opener: `hostPort`'s error was assigned to a fresh variable and never tested, so a non-integer `port` was silently ignored. Verified: `gofmt -l .` clean, `go vet ./...`, `go test ./...` green, plus a stdio smoke run of the built binary (initialize, `tools/list`, `list_profiles`, `run_query` on a redis profile, `run_redis DEL` refusal). The e2e suite was not re-run — the docker fixtures are down in this session.
- 2026-09-28: one-command install. `install.sh` (POSIX sh) fetches a release asset by OS/arch, verifies SHA-256 against the release `checksums.txt`, and installs to `$HOME/.local/bin`; it never writes MCP client configs. `.github/workflows/release.yml` builds `CGO_ENABLED=0` binaries for linux/darwin/windows (amd64, arm64, plus linux/arm) on a `v*` tag and attaches version-less assets `hq-mcp_<os>_<arch>.<tar.gz|zip>` plus `checksums.txt`, so an untagged install uses the `releases/latest/download` alias and needs no tag resolution. `go install github.com/Snarap1/hq-mcp@latest` stays broken while `go.mod` declares the bare path `hq-mcp`; a module rename is the follow-up if that path is wanted. Verified: `sh -n`/`dash -n` clean, and a real local release exercised through `HQ_MCP_DOWNLOAD_BASE` — linux tar.gz install (installed binary starts and exits 0 on EOF stdin), the windows zip branch via `bsdtar` and `unzip` shims, checksum mismatch refused, missing asset and missing-tool errors, unknown-flag exit 2.
- 2026-09-28: v0.1.0 published. `curl -fsSL .../install.sh | sh` now installs a working binary from the GitHub release. Three workflow fixes were needed to get there: the `release` job needs `actions/checkout@v4` with `fetch-depth: 0`, because `gh release create --generate-notes` reads the commit log and dies in a job that only downloaded artifacts; the build job now `rm`s the raw binary after archiving, since `upload-artifact` ships `dist/*` and the bare `hq-mcp`/`hq-mcp.exe` were being published as assets; and `install.sh` retries the download once on a checksum mismatch, because a re-uploaded asset is served stale from the CDN for a few seconds (observed live right after the tag was force-moved — a persistent mismatch still fails after the single retry). Verified end to end from a clean machine state: the published linux binary is statically linked, `initialize` reports `hq-mcp 0.1.0`, all six tools list, `list_profiles` reads a config file, and `run_query` with `DELETE FROM t` still refuses with the parity message. The stray raw-binary assets left by the earlier run were deleted from the release with `gh release delete-asset`.
- 2026-09-28: `run_redis` now allows `SET` alongside the read allowlist (user's choice; the only permitted write). `NX`/`XX`/`EX`/`PX`/`KEEPTTL` ride along as `SET` arguments, so no flag parsing was added. The allowlist map was renamed `readOnlyRedisCommands` -> `redisCommands`, and the refusal text now reads `command "<cmd>" is not allowed; this server permits read commands and SET only` — the first intentional break of parity with the Python server's wording. `run_redis`'s tool description, the README policy section, the APM skill, and `apm/.apm/skills/hq-mcp-config/references/read-only-policy.md` were updated to match; `DEL`, `EXPIRE`, `MSET`, `SETNX`, `GETSET`, `INCR`, `HSET`, `LPUSH`, `SADD` stay refused.
- 2026-09-28: dropped the config skill from the APM distribution and replaced it with one standalone user-facing guide (user's call: external users follow `docs/config-guide.md` or hand an agent the repo link, instead of installing a package). The skill moved to project scope as `.claude/skills/hq-mcp-config/SKILL.md`, rewritten as a workflow pointer, and its four `references/*.md` were folded into `docs/config-guide.md` (file choice, discovery/merge, per-adapter keys + defaults, `conn_str` URL and ODBC-list forms, ClickHouse protocol, validation, per-client registration, SQL/redis screening, troubleshooting table). The checker moved to `scripts/hq-mcp-check.py`, referenced from the guide at its repo path so it can be curled without the skill. README and AGENTS.md updated. Verified: `python3 scripts/hq-mcp-check.py --binary ./hq-mcp --config docs/plans/dev.hq-mcp.toml --no-probe` parses all 7 fixture profiles and exits 0, `gofmt -l .`/`go vet ./...` clean, `go test ./...` green.
- 2026-09-28: `apm/` came back as a registration-only package — `apm/apm.yml` (name `hq-mcp`, version `0.1.0`) declares nothing but `dependencies.mcp`: a self-defined stdio server named `hq-mcp`, `registry: false`, command `hq-mcp` from `PATH`. No skills, no `includes`/`scripts`; `apm/README.md` rewritten to say so. Renaming the package off `hq-mcp-config` matters: the lockfile no longer carries a name that promises config help it does not ship. Verified with APM CLI 0.32.0: `apm audit` clean, `apm install --dry-run --target claude` reports exactly one MCP dependency, a real install into a scratch project writes `{"mcpServers": {"hq-mcp": {"type": "stdio", "command": "hq-mcp"}}}` into `.mcp.json` and records `mcp_servers: [hq-mcp]` in the lockfile, `apm uninstall _local/apm` strips the entry and empties `mcpServers`, and the declared bare `hq-mcp` command was driven over stdio by the checker against `docs/plans/dev.hq-mcp.toml` (7 profiles, exit 0).

Deviations from the plan text, all forced by the fixtures and the drivers:
- pgx does not accept `?` placeholders: `rebindPlaceholders` rewrites them to `$1, $2, ...` for `postgres` only.
- mysql has no schema level, so `get_schema` treats `[database]` -> tables there, not -> schemas.
- The ClickHouse fixture maps 8123 to host port 58123, so `ch-dev` sets `protocol = "http"` explicitly; the port-based default (8123/8443) stays as specified.
- ODBC list-form `conn_str` elements are joined with a single space, so each element in the fixture ends with `;`; the parser only splits on `;`.
- Redis `SCAN` returns go-redis's `[cursor, keys]` pair; the e2e asserts on the keys half.
- `mssql` seeds need `CREATE DATABASE hqdb` plus `-d hqdb`; the fixture does both.

To resume on another machine: copy this directory (go.mod, go.sum, PLAN.md) or just PLAN.md, then re-run `go mod init hq-mcp && go get ...` (step 1) since the module cache does not transfer, and `bash docs/plans/fixtures.sh up` (step 11).
