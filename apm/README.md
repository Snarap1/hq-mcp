# hq-mcp-config

An [APM](https://microsoft.github.io/apm/) package with one skill: `hq-mcp-config`.

The skill teaches an agent how to write, migrate, validate, and debug [hq-mcp](https://github.com/Snarap1/hq-mcp) config files: profile tables per adapter, `default_profile`, the discovery and merge rules, `conn_str` URL and ODBC-list forms, the read-only policy, and how to register the server with an MCP client.

It ships a checker script that validates a candidate config by driving the real `hq-mcp` binary over stdio, so the agent proves a config works instead of asserting it.

## Install

Requires [APM](https://microsoft.github.io/apm/getting-started/installation/); no marketplace entry is needed, install straight from the repository path.

```sh
apm install Snarap1/hq-mcp/apm --target claude
```

`--target` accepts any harness APM supports: `claude`, `copilot`, `cursor`, `codex`, `gemini`, `opencode`, `windsurf`, `kiro`, `grok-build`.
Omit it to let APM auto-detect from the current project.
Claude, Kiro, and Grok Build receive `.claude/skills/`, `.kiro/skills/`, and `.grok/skills/`; every other target receives `.agents/skills/`.

Installing the package also registers the server: the package declares a self-defined stdio server under `dependencies.mcp`, so `apm install` writes an `hq-mcp` entry into every selected harness's native MCP config (`.mcp.json` for Claude Code, `opencode.json` for OpenCode, and so on), and `apm uninstall` removes it again.
The entry runs `hq-mcp` from `PATH`, so the binary has to be on `PATH` before the harness starts the server:

```sh
git clone https://github.com/Snarap1/hq-mcp && cd hq-mcp
go build -o ~/.local/bin/hq-mcp .
```

To pin one config file for every project, add `"env": {"HQ_MCP_CONFIG": "/abs/path/hq-mcp.toml"}` to the `hq-mcp` entry in the harness's MCP config; see `references/mcp-registration.md` for the per-client files.

The offline `apm pack` bundle ships the skill only: it contains no manifest, so the MCP server is not registered from a bundle install; register it by hand from `mcp-registration.md` in that case.

From a Git URL, in a project that already uses APM:

```yaml
# apm.yml
dependencies:
  apm:
    - Snarap1/hq-mcp/apm
```

As a standalone plugin bundle, without APM on the consumer side:

```sh
apm pack            # run in apm/, produces build/hq-mcp-config-1.0.0
apm install build/hq-mcp-config-1.0.0
```

## What the skill contains

```
.apm/skills/hq-mcp-config/
  SKILL.md                          # the workflow: six rules, then write, then verify
  references/adapters.md            # per-adapter key tables, defaults, conn_str forms
  references/config-files.md        # discovery order, merge semantics, manual validation
  references/read-only-policy.md    # SQL screening and the redis allowlist
  references/mcp-registration.md    # client registration and the smoke-call sequence
  scripts/hq-mcp-check.py           # validates a config against the real binary
```

## The checker

```sh
python3 scripts/hq-mcp-check.py --binary /path/to/hq-mcp --config ./hq-mcp.toml
```

It calls `list_profiles` and then one read probe per profile (`SELECT 1` for SQL adapters, `PING` for redis), printing `OK` or `FAIL` per check and exiting non-zero on any failure.

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

A profile that parses but cannot connect reports `FAIL probe <name>: ... connection refused`, which separates a config-shape error from a connectivity error.

## Authoring

Edit `.apm/skills/hq-mcp-config/`, then:

```sh
apm audit --file .apm/skills/hq-mcp-config/SKILL.md   # hidden-unicode and policy scan
apm install --dry-run --target claude                 # preview placement
apm pack                                              # rebuild the plugin bundle
```

Install this package into a scratch project before publishing it; that round trip is the only real test of a package.
