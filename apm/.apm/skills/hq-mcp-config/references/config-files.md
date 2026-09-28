# Config files: discovery, priority, and merge

## Candidate files

The server reads every candidate that exists, in this order, and the last one has the highest priority.

1. `$HOME/pyproject.toml`, `$HOME/.hq-mcp.toml`, `$HOME/hq-mcp.toml`
2. `$XDG_CONFIG_HOME/hq-mcp/config.toml`, `.hq-mcp.toml`, `hq-mcp.toml`
   (`$XDG_CONFIG_HOME` is `~/.config` when unset)
3. cwd: `pyproject.toml`, `.hq-mcp.toml`, `hq-mcp.toml`
4. `$HQ_MCP_CONFIG`, when set; it is appended last and wins outright

Missing files are skipped silently.
`HQ_MCP_CONFIG` is the exception: when it is set and the path does not exist, the server fails with `config file could not be found at specified path: <path>` instead of falling back.
Use it to point a test or a one-off run at a candidate config; it does not disable the other candidates, it outranks them.

A `pyproject.toml` contributes only its `[tool.hq-mcp]` table.
A top-level `keymaps` table inside `[tool.hq-mcp]` is rejected like any other unknown key, because keymap configuration is not part of this server's contract.

## Merge semantics

Merge is top-level key replacement, matching the upstream reference implementation.
A `profiles` table from a higher-priority file replaces the entire map from lower-priority files; it is not merged key by key.
A `default_profile` string replaces the previous value.

Consequence: a project-local `hq-mcp.toml` containing one profile silently removes every profile defined in `~/.config/hq-mcp/config.toml`.
Either keep project files self-contained with all the profiles the project needs, or set `HQ_MCP_CONFIG` to a single file and rely on its contents alone.

## Choosing a location

| situation | file |
| --- | --- |
| personal, all projects, one machine | `~/.config/hq-mcp/config.toml` |
| project-scoped, committed | `<repo>/hq-mcp.toml` |
| shared inside a Python project | `[tool.hq-mcp]` in `pyproject.toml` |
| testing a candidate config | `HQ_MCP_CONFIG=/path/to/candidate.toml` |
| CI or a throwaway run | `HQ_MCP_CONFIG` pointing at a generated file |

When a project is a git repository and the config holds real credentials, keep the file out of version control and note that in the answer.
A config with credentials belongs in a personal or CI-injected file; a committed config should carry placeholders only.

## Validating

Run the bundled checker against the real binary:

```sh
python3 scripts/hq-mcp-check.py --binary /path/to/hq-mcp --config ./hq-mcp.toml
```

It reports one line per profile from `list_profiles`, then probes each profile with a read-only command.
`--no-probe` validates the file shape alone; `--probe <name>` narrows the set; `--timeout SECONDS` raises the per-request timeout for slow networks.

The checker exercises the whole path that matters: discovery, merge, top-level key validation, per-adapter key validation, connection setup, and a real read query.
A `list_profiles` line proves the file parsed; the probe line proves the connection works.

## Confirming by hand over stdio

The same check without the script, useful for seeing raw JSON:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_profiles","arguments":{}}}' \
  | HQ_MCP_CONFIG=./hq-mcp.toml /path/to/hq-mcp
```

Add pauses between the writes (`{ ... ; sleep 0.3; ... ; }`) on slow machines so the server reads each line before the pipe closes.

`list_profiles` never returns passwords, which makes it the right first call to show a user that a config is wired.
