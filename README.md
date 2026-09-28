# hq-mcp

A read-only MCP server for databases, written in Go.
It reads hq-mcp's own TOML config files, so existing hq-mcp profiles work unchanged.
Supported adapters: `postgres`, `mysql`, `mssql`, `odbc`, `clickhouse`, `redis`.

The server speaks MCP over stdio: stdout is the protocol channel and every diagnostic goes to stderr.
Connections are opened per tool call and closed afterwards, so the server can run next to a live database TUI session.

## Tools

| tool | what it does |
| --- | --- |
| `list_profiles` | Lists profile names, adapters, and the default profile; never returns passwords. |
| `get_schema` | Lists database objects one level at a time; drill down with `path`. |
| `get_columns` | Lists the columns of one table: name, type, nullability. |
| `run_query` | Runs one read-only SQL statement and returns rows as JSON. |
| `run_redis` | Runs one allowlisted read-only Redis command. |
| `export_query` | Streams a read-only query into a `csv`, `json`, or `ndjson` file. |

`profile` is optional everywhere except `get_columns`: omitting it uses `default_profile`.
If no profile is given and no `default_profile` is set, the server returns `no profile specified and no default_profile is set`.

## Read-only policy

`run_query` and `export_query` screen every statement before it reaches the database.
The screening is defense in depth:

1. The statement must start with `SELECT`, `WITH`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `DESC`, `TABLE`, `INSERT`, or `UPDATE`.
2. Stacked statements are refused: one statement per call.
3. After comments, string literals, and dollar-quoted bodies are stripped, no write keyword may appear anywhere: `delete`, `drop`, `truncate`, `alter`, `create`, `replace`, `merge`, `upsert`, `grant`, `revoke`, `vacuum`, `attach`, `detach`, `copy`, `call`, `do`, `lock`, `rename`, `reindex`, `refresh`, `comment`, `nextval`, `setval`.

`INSERT` and `UPDATE` are allowed on purpose, matching the previous Python server.
One accepted consequence: `INSERT ... ON CONFLICT DO UPDATE` is refused, because `do` is a write keyword.
The SQL is sent to the database unmodified; no `LIMIT` is injected, so use the `limit` argument to bound the response.

`run_redis` is default-deny: only allowlisted read commands run (`get`, `mget`, `hgetall`, `smembers`, `scan`, `ttl`, `type`, `info`, `zrange`, `xrange`, `geosearch`, and similar).
Writes, blocking commands, `eval`/`evalsha`, `subscribe`/`monitor`, `config`, `select`, `flush*`, and `shutdown` are refused.

## Supported adapters and profile keys

| adapter | keys |
| --- | --- |
| `postgres` | `host`, `port`, `user`, `password`, `dbname` (alias `database`), `sslmode` (default `prefer`), `conn_str` |
| `mysql` | `host`, `port` (default 3306), `user`, `password`, `dbname` (alias `database`), `tls` (default `false`), `conn_str` |
| `mssql` | `host`, `port` (default 1433), `user`, `password`, `dbname` (alias `database`), `encrypt` (default `disable`), `conn_str` |
| `odbc` | `conn_str` (ODBC string, or a list of strings) |
| `clickhouse` | `host`, `port` (default 8123), `user`, `password`, `database`, `protocol` (`http` or `native`), `secure`, `conn_str` |
| `redis` | `host` (default `localhost`), `port` (default 6379), `user`, `password`, `database`, `secure`, `separator` (default `:`) |

An unknown key is rejected with the list of valid options for that adapter.
`conn_str` accepts a URL form (`postgres://`, `mysql://`, `sqlserver://`, `http://`, `https://`, `clickhouse://`); explicit profile keys win over the URL parts.
ODBC `conn_str` may be a list of strings, which shared ODBC profiles use; the elements are joined with a single space, so each element ends with `;`.

## Config

hq-mcp reads its own TOML files; no other tool's config files are touched.
The layout is the conventional database-TUI one, with hq-mcp's own file names.
Later files win by top-level key replacement, meaning a higher-priority file's `profiles` table replaces the whole map rather than merging into it.
A `pyproject.toml` contributes only its `[tool.hq-mcp]` section.
Candidates, in priority order:

1. `$HOME/pyproject.toml`, `$HOME/.hq-mcp.toml`, `$HOME/hq-mcp.toml`
2. `$XDG_CONFIG_HOME/hq-mcp/config.toml`, `.hq-mcp.toml`, `hq-mcp.toml`
3. cwd: `pyproject.toml`, `.hq-mcp.toml`, `hq-mcp.toml`
4. `$HQ_MCP_CONFIG`, when set; it is appended last and has the highest priority

Only `default_profile` and `profiles` are accepted at the top level; any other key, including a keymap table, is rejected.
To migrate, copy the `[profiles.*]` tables out of an old config into a new `hq-mcp.toml`.

```toml
default_profile = "local-pg"

[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"

[profiles.local-ch]
adapter = "clickhouse"
host = "127.0.0.1"
port = 8123
user = "app"
password = "app"
database = "appdb"

[profiles.local-redis]
adapter = "redis"
host = "127.0.0.1"
port = 6379
database = 0
```

`docs/plans/dev.hq-mcp.toml` in this repo is a loopback-only fixture config for the e2e tests; copy it to `~/.config/hq-mcp/config.toml` to get working `*-dev` profiles for the containers started by `bash docs/plans/fixtures.sh up`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Snarap1/hq-mcp/main/install.sh | sh
```

That downloads the release build for your OS and architecture, verifies its SHA-256 against the release `checksums.txt`, and installs it into `$HOME/.local/bin` as `hq-mcp` (`hq-mcp.exe` on Windows, which gets the zip asset).
Pin a build or pick another directory with flags:

```sh
curl -fsSL https://raw.githubusercontent.com/Snarap1/hq-mcp/main/install.sh | sh -s -- --version v0.1.0 --dir /usr/local/bin
```

The script needs `curl` or `wget`, `tar` (`bsdtar` or `unzip` on Windows), and one of `sha256sum`, `shasum`, `openssl`; it never needs Go.
It only installs the binary and never touches an MCP client config, so registering the server stays a separate, explicit step (see below).
APM users can point the same script at a fork with `HQ_MCP_REPO=owner/name`.

Releases are cut by pushing a `v*` tag; `.github/workflows/release.yml` builds static binaries for linux, darwin, and windows (amd64, arm64, plus linux/arm), names the assets `hq-mcp_<os>_<arch>.<tar.gz|zip>` without a version, and attaches them with `checksums.txt`.

## Build

Requires Go 1.25 (the `go.mod` directive selects it, and `GOTOOLCHAIN=auto` downloads it on first build).

```sh
go build -o hq-mcp .
```

`go build ./...` compiles every package; `go vet ./... && gofmt -l .` lints and checks formatting.

## Register with a client

```sh
claude mcp add hq-mcp -- /path/to/hq-mcp/hq-mcp
```

With the binary on `PATH`, `hq-mcp` alone also works:

```sh
claude mcp add hq-mcp -- hq-mcp
```

Or in `.claude/settings.json`:

```json
{
  "mcpServers": {
    "hq-mcp": {
      "command": "/path/to/hq-mcp/hq-mcp"
    }
  }
}
```

Then call `list_profiles` and, for example, `run_query` with `sql` set to `SELECT 1` to confirm the wiring.

## ClickHouse HTTP vs native

`clickhouse-go` speaks two protocols.
The server uses HTTP when the profile says `protocol = "http"` or when the port is 8123 or 8443, and the native protocol otherwise.
HTTP is usually what you want behind a proxy or a managed ClickHouse endpoint; native is the default on 9000.
When a container or tunnel maps a different host port onto 8123, set `protocol` explicitly, because the port-based default only recognizes 8123 and 8443.

## Redis profiles

Redis profiles live in the same TOML file as the SQL ones, which a SQL-only TUI cannot use; that tradeoff is accepted to keep one config.
`separator` (default `:`) is what `get_schema` splits key names on.
At the root, `get_schema` groups keys into `namespace` nodes (segment) and `key` nodes (a key with no further segment), with the key count in `count`; pass `include_columns` to sample the Redis type of up to 25 keys.

## Tests

```sh
go test ./...                                        # unit tests, no network
bash docs/plans/fixtures.sh up                       # five Docker fixtures on 127.0.0.1
HQ_MCP_E2E=1 go test -run TestE2E -v .               # e2e against those fixtures
bash docs/plans/fixtures.sh down                     # teardown
```

The e2e tests build the binary, write the fixture config into a temp directory, and drive it over stdio with the MCP SDK client, so no real host or credential is ever touched.

## Config-authoring skill and MCP registration (APM package)

`apm/` is an [APM](https://microsoft.github.io/apm/) package that ships one skill, `hq-mcp-config`, for writing and debugging these config files.
It carries the per-adapter key tables, the discovery and merge rules, the read-only policy, and a checker script that validates a config by driving the real binary over stdio.
The same manifest declares the server itself under `dependencies.mcp`, so one install both teaches the agent the config format and registers the server with the harness.

```sh
curl -fsSL https://raw.githubusercontent.com/Snarap1/hq-mcp/main/install.sh | sh   # the entry runs hq-mcp from PATH
apm install Snarap1/hq-mcp/apm --target claude
```

APM writes the `hq-mcp` entry into each selected harness's native MCP config and removes it again on `apm uninstall`.
See `apm/README.md` for the package layout, the checker usage, and the offline `apm pack` bundle.

