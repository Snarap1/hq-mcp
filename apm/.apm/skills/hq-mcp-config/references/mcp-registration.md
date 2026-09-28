# Registering hq-mcp with an MCP client

A config that validates is only half the setup: the client has to launch the binary, and the binary has to see the config.

## How the server finds the config

The server reads the candidate files in the order documented in `config-files.md`, with the process working directory among them.
That makes the working directory of the spawned process part of the configuration.
If a client launches the server from a directory containing an unrelated `hq-mcp.toml`, that file wins over `~/.config/hq-mcp/config.toml`.
When a user reports a profile that `list_profiles` does not show, check the client's working directory before suspecting the file they edited.

The deterministic escape hatch is `HQ_MCP_CONFIG=/abs/path/to/file.toml` in the client's server environment: it is the highest-priority candidate and a missing path is a hard error rather than a silent fallback.


## Installing the server through APM

The `hq-mcp-config` package declares this server under `dependencies.mcp`, so `apm install Snarap1/hq-mcp/apm` writes the `hq-mcp` entry into every selected harness's MCP config instead of asking the user to paste one.
Prefer that path when the project already uses APM, because `apm uninstall` also removes the entry.
The declared command is `hq-mcp` from `PATH`, so build and link the binary first (`go build -o ~/.local/bin/hq-mcp .`); a project that needs a fixed config path adds `HQ_MCP_CONFIG` to the same entry afterwards.
The offline `apm pack` bundle carries the skill only, so a bundle install still needs one of the manual registrations below.

## Claude Code

```sh
claude mcp add hq-mcp -- /abs/path/to/hq-mcp
```

With an explicit config file:

```sh
HQ_MCP_CONFIG=/abs/path/to/hq-mcp.toml claude mcp add hq-mcp -- /abs/path/to/hq-mcp
```

Or in `.claude/settings.json`:

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

## Cursor

`.cursor/mcp.json`:

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

## Codex, Gemini, OpenCode, and Copilot

All four accept a stdio server with a command and an environment map; the file name differs per client, the payload is the same.
Point `command` at the absolute path of the built binary: the server must be started with its own directory as a working directory, and any relative `HQ_MCP_CONFIG` would resolve against that directory.

Never rely on a relative binary path or a shell alias; a client spawns the process directly, without a login shell.

## Confirming the wiring

Ask the agent, in this order:

1. `list_profiles` with no arguments. Expect every profile with its adapter, and the default flagged.
   If the list is empty, the config is not being found; the server loaded no profiles at all.
2. `run_query` with `sql` set to `SELECT 1` on the default profile. Expect one row.
   A refusal here is the read-only policy or a screening issue, not a config issue; a connection error is a config issue.
3. For redis, `run_redis` with `command` set to `PING`.

## Operational notes

- stdout is the protocol channel: every diagnostic goes to stderr, and a client that merges the streams will corrupt the session.
- The server opens a fresh connection per tool call and closes it afterwards, so it can run alongside a live database TUI session without sharing state.
- Because connections are per call, an unreachable database surfaces on the first query, not at startup. `list_profiles` alone does not prove connectivity; that is what the bundled checker's probe step is for.
- Requires Go 1.25 to build (`go build -o hq-mcp .`); the binary has no runtime dependencies beyond the database and, for `odbc` profiles, nothing at all, because the ODBC driver is never loaded.
