# hq-mcp (APM)

An [APM](https://microsoft.github.io/apm/) package that does one thing: it registers the [hq-mcp](https://github.com/Snarap1/hq-mcp) read-only database MCP server with your harness.

It ships no skills and no config material.
Configuration is documented in the repository guide, [`docs/config-guide.md`](../docs/config-guide.md), and a user can be pointed at that link instead of installing anything.

## Install

Requires [APM](https://microsoft.github.io/apm/getting-started/installation/); no marketplace entry is needed, install straight from the repository path.

```sh
apm install Snarap1/hq-mcp/apm --target claude
```

`--target` accepts any harness APM supports: `claude`, `copilot`, `cursor`, `codex`, `gemini`, `opencode`, `windsurf`, `kiro`, `grok-build`.
Pass it explicitly: without it apm resolves the harness from the filesystem and exits rather than guessing.

Project scope writes the harness config inside the current repository, user scope (`-g`/`--global`) writes the user-level config and applies to every project on the machine:

```sh
apm install Snarap1/hq-mcp/apm --target claude    # project scope, <repo>/.mcp.json
apm install Snarap1/hq-mcp/apm --target codex     # project scope, <repo>/.codex/config.toml
apm install -g Snarap1/hq-mcp/apm --target claude  # user scope, ~/.claude.json
apm install -g Snarap1/hq-mcp/apm --target codex   # user scope, ~/.codex/config.toml
```

The harness directory and its config file are created if missing.
`--dry-run` previews without writing, and `apm uninstall` strips the entry back out.

The manifest declares a self-defined stdio server under `dependencies.mcp`, so `apm install` writes an `hq-mcp` entry into every selected harness's native MCP config (`.mcp.json` for Claude Code, `opencode.json` for OpenCode, and so on), and `apm uninstall` removes it again.

The entry runs `hq-mcp` from `PATH`, so the binary has to be on `PATH` before the harness starts the server:

```sh
git clone https://github.com/Snarap1/hq-mcp && cd hq-mcp
go build -o ~/.local/bin/hq-mcp .
```

To pin one config file for every project, add `"env": {"HQ_MCP_CONFIG": "/abs/path/hq-mcp.toml"}` to the `hq-mcp` entry in the harness's MCP config; `../docs/config-guide.md` has the per-client files and the config rules.

From a Git URL, in a project that already uses APM:

```yaml
# apm.yml
dependencies:
  apm:
    - Snarap1/hq-mcp/apm
```

## Authoring

```sh
apm audit                 # hidden-unicode and policy scan
apm install --dry-run --target claude   # preview placement
```

Install into a scratch project before publishing; that round trip is the only real test of a package.
