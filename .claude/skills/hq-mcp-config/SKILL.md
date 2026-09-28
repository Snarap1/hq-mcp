---
name: hq-mcp-config
description: Use when writing, editing, migrating, or debugging an hq-mcp config file (hq-mcp.toml, .hq-mcp.toml, [tool.hq-mcp] in pyproject.toml, ~/.config/hq-mcp/config.toml), or when registering the hq-mcp server with an MCP client. Points at docs/config-guide.md, the authoritative repo guide, and scripts/hq-mcp-check.py, the validator that drives the real binary over stdio.
---

# hq-mcp config authoring

hq-mcp is a read-only MCP server for databases, written in Go, configured entirely by its own TOML files.
No other tool's config files are ever read.

The reference for this skill is `docs/config-guide.md` at the repository root: file locations, discovery and merge order, per-adapter key tables, `conn_str` URL and ODBC-list forms, the read-only policy, client registration, and a troubleshooting table.
Read it before writing anything; this file only carries the workflow.

## Workflow

1. Gather the connection facts: adapter, host, port, user, password, database, TLS or encryption requirement, and for redis the key separator convention.
2. Pick the file. `~/.config/hq-mcp/config.toml` for personal machine-wide profiles, a project-local `hq-mcp.toml` for repo-scoped ones.
   See section 1 of the guide for the full discovery and priority order; the merge is top-level key replacement, so a project file's `profiles` table wipes every lower-priority profile.
3. Write the profile: `[profiles.<name>]` with a required `adapter`, and only keys valid for that adapter.
   Copy the template from section 3 of the guide.
4. Decide `default_profile` only when the user wants one; without it the server returns `no profile specified and no default_profile is set`.
5. Validate against the real binary, not by eye:

```sh
python3 scripts/hq-mcp-check.py --binary /path/to/hq-mcp --config ./hq-mcp.toml
```

It calls `list_profiles`, then one cheap read probe per profile (`SELECT 1` for SQL adapters, `PING` for redis), prints `OK`/`FAIL` per check, and exits non-zero on any failure.
`--no-probe` checks the file shape alone; `--probe <name>` narrows the set.

6. Tell the user how to confirm from their own client: `list_profiles`, then `run_query` with `SELECT 1`, and `run_redis` with `PING` for redis.
   Section 6 of the guide has the per-client registration snippets.

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

## When a config is right but queries fail

The config is not the problem; the screening rules are.
`run_query` and `export_query` require the statement to start with `SELECT`, `WITH`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `DESC`, `TABLE`, `INSERT`, or `UPDATE`, refuse stacked statements, and refuse any write keyword that survives comment and string stripping.
No `LIMIT` is injected, so bound the response with the `limit` argument.
`run_redis` is default-deny; only the read allowlist plus `set` itself run.
Section 7 of the guide carries the exact keyword and command lists; check them before telling a user that a query should work.
