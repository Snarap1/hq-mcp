# Configuring hq-mcp

A guide to writing, validating, and debugging hq-mcp config files.
Everything here is what the server actually implements; nothing depends on an agent skill or a package manager.

hq-mcp is configured entirely by its own TOML files.
It never reads another tool's config files.

## 1. Pick a file

| situation | file |
| --- | --- |
| personal, all projects, one machine | `~/.config/hq-mcp/config.toml` |
| project-scoped, committed | `<repo>/hq-mcp.toml` |
| shared inside a Python project | `[tool.hq-mcp]` in `pyproject.toml` |
| testing a candidate config | `HQ_MCP_CONFIG=/path/to/candidate.toml` |
| CI or a throwaway run | `HQ_MCP_CONFIG` pointing at a generated file |

### Discovery and priority

The server reads every candidate that exists; the last one wins.

1. `$HOME/pyproject.toml`, `$HOME/.hq-mcp.toml`, `$HOME/hq-mcp.toml`
2. `$XDG_CONFIG_HOME/hq-mcp/config.toml`, `.hq-mcp.toml`, `hq-mcp.toml` (`$XDG_CONFIG_HOME` is `~/.config` when unset)
3. cwd: `pyproject.toml`, `.hq-mcp.toml`, `hq-mcp.toml`
4. `$HQ_MCP_CONFIG`, when set; appended last, so it has the highest priority

Missing files are skipped silently.
`HQ_MCP_CONFIG` is the exception: a set-but-missing path is a hard error, `config file could not be found at specified path: <path>`, not a fallback.

A `pyproject.toml` contributes only its `[tool.hq-mcp]` table; every other section is ignored.

### Merge semantics

Merge is **top-level key replacement, not a deep merge**.
A higher-priority file's `profiles` table replaces the whole map from every lower-priority file.
A `default_profile` string replaces the previous value.

Consequence: a project-local `hq-mcp.toml` with one profile silently removes every profile in `~/.config/hq-mcp/config.toml`.
Keep each file self-contained, or point `HQ_MCP_CONFIG` at one file and rely on its contents alone.

### Credentials

Passwords live in the file in plain text.
Keep a real-credential config out of version control.
A committed example should carry placeholders only.

## 2. The file shape

Only two top-level keys are accepted: `default_profile` and `profiles`.
Anything else, `keymaps` included, is rejected with
`hq-mcp config files must only contain default_profile and profiles tables (got <key>)`.

```toml
default_profile = "local-pg"

[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"
```

Profile names are TOML table names, so a name with a dot or a space must be quoted: `[profiles."analytics.eu"]`.
Prefer plain lowercase-with-dashes names.

Every profile needs `adapter`.
Valid values: `postgres`, `mysql`, `mssql`, `odbc`, `clickhouse`, `redis`.
A profile with no `adapter` reads as `duckdb` and fails to open; there is no duckdb adapter.

`default_profile` is optional.
Without it, every call without an explicit `profile` argument returns
`no profile specified and no default_profile is set`.

### Migration from an older config

Copy the `[profiles.*]` tables into a new `hq-mcp.toml`.
Do not rename the file: hq-mcp never reads another tool's config, so a profile the user edits there will not reach the server.
Drop foreign-only keys, in particular a top-level `keymaps` table, because they are rejected rather than ignored.
Redis profiles may be new: redis lives in the same TOML file as the SQL profiles, and that tradeoff is deliberate to keep one config.

## 3. Adapter keys

Every key inside a profile must be valid for that adapter.
A typo such as `hostname` fails with
`unknown option "hostname" for adapter "postgres"; valid options: adapter, conn_str, database, dbname, host, password, port, sslmode, user`.

`database` is an alias of `dbname` for `postgres`, `mysql`, and `mssql`; `dbname` wins when both are present.
`conn_str` is accepted by every adapter except `redis`.

| adapter | keys | defaults |
| --- | --- | --- |
| `postgres` | `host`, `port`, `user`, `password`, `dbname` (`database`), `sslmode`, `conn_str` | host `localhost`, port `5432`, `sslmode = "prefer"` |
| `mysql` | `host`, `port`, `user`, `password`, `dbname` (`database`), `tls`, `conn_str` | host `localhost`, port `3306`, `tls = "false"` |
| `mssql` | `host`, `port`, `user`, `password`, `dbname` (`database`), `encrypt`, `conn_str` | host `localhost`, port `1433`, `encrypt = "disable"` |
| `odbc` | `conn_str` only | required, no default |
| `clickhouse` | `host`, `port`, `user`, `password`, `database` (`dbname` fallback), `protocol`, `secure`, `conn_str` | host `localhost`, port `8123` |
| `redis` | `host`, `port`, `user`, `password`, `database`, `secure`, `separator` | host `localhost`, port `6379`, `database = 0`, `separator = ":"` |

An empty postgres host becomes `localhost` rather than a unix socket.

`postgres` requires no `dbname`.
Omit it and the startup message carries no database, so the server connects to the login's default database, exactly as an mssql profile without `dbname` does.
That is the cheap way to hold one profile per host instead of one per database: the cross-database reads then go through explicit `dbname` qualifiers in the SQL.
The mysql `tls` value is passed to the driver as its `tls` parameter, so use the driver's registered names (`true`, `skip-verify`, `preferred`).
`secure = true` (clickhouse, redis) turns on TLS with a minimum of TLS 1.2.
`separator` is what `get_schema` splits redis key names on; match the instance's key convention.

### Examples

```toml
[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"

[profiles.local-my]
adapter = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
password = "app"
dbname = "appdb"

[profiles.local-ms]
adapter = "mssql"
host = "127.0.0.1"
port = 1433
user = "sa"
password = "app"
dbname = "appdb"
encrypt = "disable"

[profiles.local-ch]
adapter = "clickhouse"
host = "127.0.0.1"
port = 8123
protocol = "http"
user = "app"
password = "app"
database = "appdb"

[profiles.local-redis]
adapter = "redis"
host = "127.0.0.1"
port = 6379
database = 0
separator = ":"
```

`docs/plans/dev.hq-mcp.toml` in this repo is a loopback-only fixture config for the e2e tests; copy it to `~/.config/hq-mcp/config.toml` for working `*-dev` profiles against the containers started by `bash docs/plans/fixtures.sh up`.

## 4. `conn_str` forms

### URL form

For `postgres`, `mysql`, `mssql`, and `clickhouse`, `conn_str` may be a URL whose scheme is `postgres://`, `mysql://`, `sqlserver://`, `http://`, `https://`, or `clickhouse://`.
The path sets the database (`database` for clickhouse, `dbname` otherwise); the authority sets `host`, `port`, `user`, `password`.
URL parts only fill keys absent from the profile: explicit keys always win.

```toml
[profiles.url-pg]
adapter = "postgres"
conn_str = "postgres://app:app@127.0.0.1:5432/appdb?sslmode=require"
sslmode = "verify-full"
```

### ODBC form

An `odbc` profile takes `conn_str` and nothing else; without it the server returns `adapter "odbc" requires conn_str`.

The ODBC driver is never loaded: the server parses the string itself and connects over TDS with go-mssqldb.
Recognized keys, case-insensitive: `Driver` (read but unused), `Server` / `Data Source` / `Addr` / `Address` / `Network Address` (`host[,port]`), `Database` / `Initial Catalog`, `Uid` / `User` / `UserName`, `Pwd` / `Password`, `Encrypt`.
A missing port becomes `1433`; a missing `Server` is the error `ODBC conn_str has no Server: <conn_str>`.
`Encrypt` maps as: `no`, `false`, `0`, `optional` to `disable`; `strict` to `strict`; anything else, including `yes` and `true`, to `true`.

`conn_str` may be a string or a list of strings, joined with a single space.
Real profiles use the list form, and each element ends with `;`:

```toml
[profiles.local-odbc]
adapter = "odbc"
conn_str = [
  "Driver={ODBC Driver 18 for SQL Server};",
  "Server=127.0.0.1,1433;",
  "Database=appdb;",
  "Uid=sa;",
  "Pwd=app;",
  "Encrypt=no;",
]
```

### ClickHouse protocol

`protocol` is `http`, `https`, or `native`; anything else fails with `clickhouse protocol must be "http" or "native" (got "...")`.
When `protocol` is absent the port decides: `8123` and `8443` mean HTTP, everything else native.
Set `protocol = "http"` explicitly whenever a container or tunnel maps some other host port onto 8123, because the port heuristic only recognizes those two numbers.

## 5. Validate against the real binary

`scripts/hq-mcp-check.py` in this repository drives the server over stdio: it calls `list_profiles`, then one cheap read probe per profile (`SELECT 1` for SQL adapters, `PING` for redis).

```sh
python3 scripts/hq-mcp-check.py --binary /path/to/hq-mcp --config ./hq-mcp.toml
```

| flag | effect |
| --- | --- |
| `--binary PATH` | the `hq-mcp` binary to test (required) |
| `--config PATH` | sets `HQ_MCP_CONFIG`, the highest-priority config source |
| `--probe NAME` | probe only these profiles; repeatable |
| `--no-probe` | validate the file shape without connecting |
| `--timeout SECONDS` | per-request timeout, default 20 |

Typical output:

```
OK    profile pg-dev adapter=postgres (default)
OK    profile redis-dev adapter=redis
OK    probe pg-dev: SELECT 1 -> {"columns": ["?column?"], "row_count": 1, ...}
OK    probe redis-dev: PING -> {"command": "PING", "result": "PONG"}
```

Exit status is 0 only when every check passed, so it drops into CI or a pre-commit hook.
A profile that parses but cannot connect reports `FAIL probe <name>: ... connection refused`, which separates a config-shape error from a connectivity error.

The same check by hand, without the script:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_profiles","arguments":{}}}' \
  | HQ_MCP_CONFIG=./hq-mcp.toml /path/to/hq-mcp
```

Add pauses between the writes (`{ ... ; sleep 0.3; ... ; }`) on slow machines so the server reads each line before the pipe closes.

## 6. Register with a client

The client has to launch the binary, and the binary has to see the config.
The server's working directory is part of discovery, so a client launched from a directory containing an unrelated `hq-mcp.toml` makes that file win over `~/.config/hq-mcp/config.toml`.
The deterministic escape hatch is an absolute `HQ_MCP_CONFIG` in the server's environment.

Claude Code:

```sh
claude mcp add hq-mcp -- /abs/path/to/hq-mcp
HQ_MCP_CONFIG=/abs/path/to/hq-mcp.toml claude mcp add hq-mcp -- /abs/path/to/hq-mcp
```

`.claude/settings.json`:

```json
{
  "mcpServers": {
    "hq-mcp": {
      "command": "/abs/path/to/hq-mcp",
      "env": {
        "HQ_MCP_CONFIG": "/abs/path/to/hq-mcp.toml"
      }
    }
  }
}
```

Codex reads `~/.codex/config.toml` at user scope, and `.codex/config.toml` in a project for a trusted project.
It uses TOML, not JSON, and a different table name:

```toml
# <repo>/.codex/config.toml  (project scope)
[mcp_servers.hq-mcp]
command = "/abs/path/to/hq-mcp"
env = { HQ_MCP_CONFIG = "/abs/path/to/hq-mcp.toml" }
```

`env` is optional; drop it to use the normal discovery order.
`startup_timeout_sec` (default 10) and `tool_timeout_sec` (default 60) sit on the same table when a slow database needs more headroom.

Cursor uses `.cursor/mcp.json` with the same JSON payload as `.mcp.json`.
Gemini, OpenCode, and Copilot also take a stdio server with a `command` and an `env` map; only the file name differs.
Point `command` at the absolute path of the built binary: the process is spawned directly, without a login shell, so a relative path or shell alias never resolves.

With [APM](https://microsoft.github.io/apm/) installed, the same registration comes from the package in this repository, which declares only the server:

```sh
apm install Snarap1/hq-mcp/apm --target claude   # project scope, writes <repo>/.mcp.json
apm install Snarap1/hq-mcp/apm --target codex    # project scope, writes <repo>/.codex/config.toml
apm install -g Snarap1/hq-mcp/apm --target claude   # user scope, writes ~/.claude.json
apm install -g Snarap1/hq-mcp/apm --target codex    # user scope, writes ~/.codex/config.toml
```

`-g`/`--global` is the only way to select user scope, and `--target` should always be passed: without it apm resolves the harness from the filesystem and exits rather than guessing.
Add `--dry-run` to preview, and `apm uninstall` to strip the entry back out.
The written command is bare `hq-mcp` from `PATH`, so the binary has to be linked before the harness starts; add `env` to the entry afterwards to pin one config file.
apm creates the harness directory and the config file when they are missing, so nothing has to exist beforehand.

Then confirm in this order:

1. `list_profiles` with no arguments; expect every profile with its adapter and the default flagged. An empty list means the config was not found at all.
2. `run_query` with `sql` set to `SELECT 1` on the default profile; expect one row. A refusal here is the read-only policy, a connection error is a config issue.
3. For redis, `run_redis` with `command` set to `PING`.

## 7. When a config is right but queries fail

The config is not the problem; the screening rules are.

`run_query` and `export_query` screen every statement before it reaches the database.
Comments, string literals, quoted identifiers, and dollar-quoted bodies are stripped first, so text inside them cannot trigger or satisfy a keyword.

1. The statement must start with `SELECT`, `WITH`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `DESC`, `TABLE`, `INSERT`, or `UPDATE`; anything else is refused with `statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed`.
2. One statement per call; a `;` separating two statements is refused with `multiple statements are not allowed; send one read-only query`.
3. No write keyword may survive anywhere: `delete`, `drop`, `truncate`, `alter`, `create`, `replace`, `merge`, `upsert`, `grant`, `revoke`, `vacuum`, `attach`, `detach`, `copy`, `call`, `do`, `lock`, `rename`, `reindex`, `refresh`, `comment`, `nextval`, `setval` — refused with `statement contains write keyword(s): <sorted list>`.

`INSERT` and `UPDATE` are allowed on purpose, for parity with the Python server this replaced.
`INSERT ... ON CONFLICT DO UPDATE` is therefore refused, because `do` is a write keyword; so is a data-modifying CTE, and so is a column or table whose name is a whole-word keyword such as `comment`.
None of these is a bug to work around in the server.

The SQL is sent to the database unmodified; no `LIMIT` is injected, so bound the response with the `limit` argument.
`run_query` defaults to 50 rows, `export_query` to unlimited when `limit` is 0.

`run_redis` is default-deny: the leading word of `command` must be on the allowlist, or the call is refused with `command "<cmd>" is not allowed; this server permits read commands and SET only`.
An empty command is refused with `empty command`.

Allowed: `ping`, `time`, `info`, `dbsize`, `scan`, `keys`, `type`, `exists`, `ttl`, `pttl`, `strlen`, `get`, `getrange`, `getbit`, `mget`, `bitcount`, `lpos`, `hget`, `hgetall`, `hkeys`, `hlen`, `hmget`, `hvals`, `hscan`, `lrange`, `lindex`, `llen`, `scard`, `smembers`, `sismember`, `smismember`, `srandmember`, `sscan`, `zcard`, `zcount`, `zlexcount`, `zrange`, `zrangebyscore`, `zrangebylex`, `zrevrange`, `zrank`, `zrevrank`, `zscore`, `zmscore`, `zscan`, `xlen`, `xrange`, `xrevrange`, `xinfo`, `object`, `memory`, `randomkey`, `geopos`, `geodist`, `geohash`, `geosearch`, `set`.

`set` is the one allowed write, with its `NX`, `XX`, `EX`, `PX`, and `KEEPTTL` flags; nothing else that writes is allowed.
Refused by omission: every other write (`del`, `expire`, `mset`, `setnx`, `getset`, `incr`, `hset`, `lpush`, `sadd`, ...), all blocking commands, `eval` and `evalsha`, `subscribe` and `monitor`, `config`, `select`, `flush*`, and `shutdown`.

Inline arguments in `command` are split on whitespace, so quoting is not honoured: `GET "user:1000"` looks up a key that includes literal quote characters.
Put arguments with spaces in the `args` array, which is passed through verbatim.

Cross-adapter guardrails: `run_query`/`export_query` on a redis profile return `<tool> is not available for redis; use run_redis`; `run_redis` on a SQL profile returns `run_redis is not available for <adapter>`; `get_columns` on a redis profile returns `get_columns is not available for redis; use get_schema`.

## Troubleshooting

| symptom | cause |
| --- | --- |
| `hq-mcp config files must only contain ... (got keymaps)` | a foreign top-level key; only `default_profile` and `profiles` exist |
| `unknown option "<key>" for adapter "<a>"; valid options: ...` | typo, or a key from another adapter |
| `adapter "odbc" requires conn_str` | an `odbc` profile with no `conn_str` |
| `config file could not be found at specified path: <path>` | `HQ_MCP_CONFIG` points at a file that does not exist |
| a profile from `~/.config` is missing | a higher-priority `profiles` table replaced the whole map |
| `list_profiles` is empty | the client launched the server from an unexpected working directory, or no candidate file has profiles |
| `no profile specified and no default_profile is set` | no `default_profile`, and the call passed no `profile` |
| `statement contains write keyword(s): ...` | the read-only policy, not the config |
