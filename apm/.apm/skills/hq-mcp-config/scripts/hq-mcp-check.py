#!/usr/bin/env python3
"""Validate an hq-mcp config by driving the real server binary over stdio.

Speaks MCP JSON-RPC by hand: initialize, notifications/initialized, then
list_profiles, then one cheap read probe per profile (SELECT 1 for SQL
adapters, PING for redis).

Usage:
    hq-mcp-check.py --binary /path/to/hq-mcp [--config PATH] [--probe NAME ...] [--timeout SECONDS]

Exit status is 0 when the config loads and every selected probe succeeds.
"""

from __future__ import annotations

import argparse
import json
import os
import select
import subprocess
import sys
import time

SQL_PROBE = "SELECT 1"
REDIS_PROBE = "PING"
REDIS_ADAPTERS = {"redis"}


class Server:
    """One hq-mcp process driven over stdin/stdout."""

    def __init__(self, binary: str, config: str | None, timeout: float) -> None:
        env = dict(os.environ)
        if config:
            env["HQ_MCP_CONFIG"] = os.path.abspath(config)
        self.timeout = timeout
        self.proc = subprocess.Popen(
            [binary],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            text=True,
            bufsize=1,
        )
        self.next_id = 0

    def send(self, payload: dict) -> None:
        assert self.proc.stdin is not None
        self.proc.stdin.write(json.dumps(payload) + "\n")
        self.proc.stdin.flush()

    def read(self) -> str:
        assert self.proc.stdout is not None
        deadline = time.monotonic() + self.timeout
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(f"no response within {self.timeout}s")
            ready, _, _ = select.select([self.proc.stdout], [], [], remaining)
            if not ready:
                raise TimeoutError(f"no response within {self.timeout}s")
            line = self.proc.stdout.readline()
            if line == "":
                raise EOFError(self.stderr_text() or "server closed stdout")
            if line.strip():
                return line

    def stderr_text(self) -> str:
        if self.proc.stderr is None:
            return ""
        try:
            import fcntl
            import os as _os

            fd = self.proc.stderr.fileno()
            flags = fcntl.fcntl(fd, fcntl.F_GETFL)
            fcntl.fcntl(fd, fcntl.F_SETFL, flags | _os.O_NONBLOCK)
            return (self.proc.stderr.read() or "").strip()
        except Exception:
            return ""

    def request(self, method: str, params: dict) -> dict:
        self.next_id += 1
        want = self.next_id
        self.send({"jsonrpc": "2.0", "id": want, "method": method, "params": params})
        while True:
            msg = json.loads(self.read())
            if msg.get("id") != want:
                continue
            if "error" in msg:
                raise RuntimeError(msg["error"].get("message", str(msg["error"])))
            return msg.get("result", {})

    def notify(self, method: str, params: dict | None = None) -> None:
        self.send({"jsonrpc": "2.0", "method": method, "params": params or {}})

    def call_tool(self, name: str, arguments: dict) -> dict:
        result = self.request("tools/call", {"name": name, "arguments": arguments})
        if result.get("isError"):
            raise RuntimeError(tool_text(result) or f"{name} failed")
        return result

    def close(self) -> None:
        try:
            if self.proc.stdin:
                self.proc.stdin.close()
        except Exception:
            pass
        try:
            self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.proc.kill()


def tool_text(result: dict) -> str:
    parts = [c.get("text", "") for c in result.get("content", []) if c.get("type") == "text"]
    return "\n".join(p for p in parts if p).strip()


def tool_json(result: dict) -> dict:
    structured = result.get("structuredContent")
    if isinstance(structured, dict) and structured:
        return structured
    text = tool_text(result)
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        raise RuntimeError(f"tool returned non-JSON text: {text}")
    return parsed if isinstance(parsed, dict) else {"value": parsed}


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--binary", required=True, help="path to the hq-mcp binary")
    ap.add_argument("--config", help="config file to test; sets HQ_MCP_CONFIG (highest priority)")
    ap.add_argument("--probe", action="append", metavar="NAME", help="probe only this profile (repeatable)")
    ap.add_argument("--no-probe", action="store_true", help="only load the config and list profiles")
    ap.add_argument("--timeout", type=float, default=20.0, help="per-request timeout in seconds")
    args = ap.parse_args()

    if not os.path.isfile(args.binary) or not os.access(args.binary, os.X_OK):
        print(f"error: {args.binary} is not an executable file; build it with `go build -o hq-mcp .`", file=sys.stderr)
        return 2

    server = Server(args.binary, args.config, args.timeout)
    failures = 0
    try:
        server.request(
            "initialize",
            {
                "protocolVersion": "2024-11-05",
                "capabilities": {},
                "clientInfo": {"name": "hq-mcp-check", "version": "1.0.0"},
            },
        )
        server.notify("notifications/initialized")

        listed = tool_json(server.call_tool("list_profiles", {}))
        profiles = listed.get("profiles") or []
        if not profiles:
            print("FAIL  config loaded but exposes no profiles", file=sys.stderr)
            return 1
        for p in profiles:
            flag = " (default)" if p.get("is_default") else ""
            print(f"OK    profile {p['name']} adapter={p['adapter']}{flag}")

        wanted = set(args.probe or [])
        if wanted:
            known = {p["name"] for p in profiles}
            for missing in sorted(wanted - known):
                print(f"FAIL  profile {missing} is not in the config", file=sys.stderr)
                failures += 1

        for p in profiles:
            name = p["name"]
            if args.no_probe or (wanted and name not in wanted):
                continue
            try:
                if p["adapter"] in REDIS_ADAPTERS:
                    out = tool_json(server.call_tool("run_redis", {"profile": name, "command": REDIS_PROBE}))
                else:
                    out = tool_json(server.call_tool("run_query", {"profile": name, "sql": SQL_PROBE}))
                print(f"OK    probe {name}: {SQL_PROBE if p['adapter'] not in REDIS_ADAPTERS else REDIS_PROBE} -> {json.dumps(out)}")
            except Exception as exc:  # noqa: BLE001 - report, keep probing the rest
                print(f"FAIL  probe {name}: {exc}", file=sys.stderr)
                failures += 1
    except Exception as exc:  # noqa: BLE001 - config-level failure, nothing else to report
        print(f"FAIL  {exc}", file=sys.stderr)
        return 1
    finally:
        server.close()

    if failures:
        print(f"{failures} check(s) failed", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
