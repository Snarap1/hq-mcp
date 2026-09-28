---
name: hq-mcp-config
description: Use when writing, editing, migrating, or debugging an hq-mcp config file (hq-mcp.toml, .hq-mcp.toml, [tool.hq-mcp] in pyproject.toml, ~/.config/hq-mcp/config.toml) or registering the hq-mcp server with an MCP client. Covers profile tables per adapter, default_profile, config discovery and merge order, conn_str URL and ODBC-list forms, and validating a config against a live binary.
---

# hq-mcp config authoring

hq-mcp is a read-only MCP server for databases, written in Go.
It is configured entirely by TOML files that follow the conventional database-TUI layout, but with hq-mcp's own names; no other tool's config files are ever read.

This skill is about getting a config right: the file, the profile table, and proof that the server can actually open the connection.

## Rules that break configs

Six rules cause almost every failure.
State them, then write the file.

1. The top level accepts exactly two keys: `default_profile` and `profiles`.
   Anything else, including a `keymaps` table, is rejected with `hq-mcp config files must only contain default_profile and profiles tables (got <key>)`.
2. Every profile needs `adapter`.
   Valid values: `postgres`, `mysql`, `mssql`, `odbc`, `clickhouse`, `redis`.
   A profile with no `adapter` key reads as `duckdb` and fails to open; there is no duckdb adapter.
3. Every key inside a profile must be valid for that adapter.
   A typo such as `hostname` fails with `unknown option "hostname" for adapter "postgres"; valid options: adapter, conn_str, database, dbname, host, password, port, sslmode, user`.
4. Merge is top-level key replacement, not deep merge.
   A higher-priority file's `profiles` table replaces the whole map, so a project file with one profile wipes every home-directory profile.
   Keep each file self-contained or accept the override deliberately.
5. `pyproject.toml` contributes only its `[tool.hq-mcp]` section; every other section is ignored.
6. Passwords live in the file in plain text.
   Never echo them into a committed config, a log, a chat message, or a committed example; use placeholders and point the user at an env-var-friendly local file.

## The workflow

1. Gather the connection facts: adapter, host, port, user, password, database, TLS or encryption requirement, and for redis the key separator convention.
2. Pick the file. Default to `~/.config/hq-mcp/config.toml` for personal machine-wide profiles, and a project-local `hq-mcp.toml` for repo-scoped ones.
   LOAD references/config-files.md for the full discovery and priority order.
3. Write the profile. LOAD references/adapters.md for the per-adapter key tables, defaults, `conn_str` URL forms, and the ODBC list form; copy the matching template from there.
4. Decide `default_profile` only when the user wants one.
   It is what every tool uses when the `profile` argument is omitted; without it the server returns `no profile specified and no default_profile is set`.
5. Validate against the real binary, not by eye:

```sh
python3 scripts/hq-mcp-check.py --binary /path/to/hq-mcp --config ./hq-mcp.toml
```

The script drives the server over stdio: it calls `list_profiles`, then runs one cheap read probe per profile (`SELECT 1` for SQL adapters, `PING` for redis).
A profile that loads but cannot connect shows up as `FAIL probe <name>: ... connection refused`, which separates a config-shape error from a connectivity error.
Add `--no-probe` to validate the file shape without touching the databases, or `--probe <name>` (repeatable) to check specific profiles.
Exit status is 0 only when every check passed, so it drops straight into CI or a pre-commit hook.
6. Tell the user how to confirm from their own client: `list_profiles`, then `run_query` with `SELECT 1`.
   In a project that already uses APM, register the server with `apm install Snarap1/hq-mcp/apm` instead of hand-editing a client config; LOAD references/mcp-registration.md for that path and for the per-client snippets it falls back to.

Do not claim a config works until step 5 has run and printed `OK` for the profiles it touched.
If the binary is not built, build it first with `go build -o hq-mcp .` from the hq-mcp repository, or ask the user for its path.

## Minimal correct config

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

## Migrating an old config

Copy the `[profiles.*]` tables into a new `hq-mcp.toml`.
Do not rename the file: hq-mcp never reads another tool's config, so a profile the user edits there will not reach the server.
Drop foreign-only keys, in particular `keymaps` at the top level, because they are rejected outright rather than ignored.
Redis profiles may be new: redis lives in the same TOML file as the SQL profiles, which a SQL-only TUI cannot use, and that tradeoff is deliberate to keep one config.

## When a config is right but queries fail

The config is not the problem; the screening rules are.
`run_query` and `export_query` require the statement to start with `SELECT`, `WITH`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `DESC`, `TABLE`, `INSERT`, or `UPDATE`, refuse stacked statements, and refuse any write keyword that survives comment and string stripping.
No `LIMIT` is injected, so bound the response with the `limit` argument.
`run_redis` is default-deny; only the read-only allowlist runs.
LOAD references/read-only-policy.md for the exact keyword and command lists before telling a user that a query should work.
