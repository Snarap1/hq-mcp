// Package rediscmd runs one allowlisted read-only Redis command.
package rediscmd

import (
	"context"
	"fmt"
	"strings"

	"hq-mcp/internal/adapters"
	"hq-mcp/internal/values"
)

// readOnlyRedisCommands is the default-deny allowlist for run_redis.
// Writes, blocking commands, scripting (eval/evalsha), pubsub/monitor, and
// config/select/flush/shutdown are deliberately absent.
var readOnlyRedisCommands = map[string]bool{
	"ping": true, "time": true, "info": true, "dbsize": true,
	"scan": true, "keys": true, "type": true, "exists": true, "ttl": true, "pttl": true,
	"strlen": true, "get": true, "getrange": true, "getbit": true, "mget": true, "bitcount": true,
	"lpos": true, "hget": true, "hgetall": true, "hkeys": true, "hlen": true, "hmget": true,
	"hvals": true, "hscan": true, "lrange": true, "lindex": true, "llen": true,
	"scard": true, "smembers": true, "sismember": true, "smismember": true,
	"srandmember": true, "sscan": true,
	"zcard": true, "zcount": true, "zlexcount": true, "zrange": true, "zrangebyscore": true,
	"zrangebylex": true, "zrevrange": true, "zrank": true, "zrevrank": true, "zscore": true,
	"zmscore": true, "zscan": true,
	"xlen": true, "xrange": true, "xrevrange": true, "xinfo": true,
	"object": true, "memory": true, "randomkey": true,
	"geopos": true, "geodist": true, "geohash": true, "geosearch": true,
}

// CommandViolation returns a reason the command line is refused, or "" if
// the leading command is on the read-only allowlist.
func CommandViolation(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "empty command"
	}
	if !readOnlyRedisCommands[strings.ToLower(fields[0])] {
		return fmt.Sprintf("command %q is not allowed; this server permits read-only Redis commands only", fields[0])
	}
	return ""
}

// Run executes an allowlisted command with string arguments and returns the
// JSON-safe result.
func Run(ctx context.Context, c *adapters.Redis, command string, args []string) (any, error) {
	fields := strings.Fields(command)
	if violation := CommandViolation(command); violation != "" {
		return nil, fmt.Errorf("Refused (read-only server): %s", violation)
	}
	argv := make([]any, 0, len(fields)+len(args))
	for _, f := range fields[1:] {
		argv = append(argv, f)
	}
	for _, a := range args {
		argv = append(argv, a)
	}
	res, err := c.Redis().Do(ctx, append([]any{strings.ToLower(fields[0])}, argv...)...).Result()
	if err != nil {
		return nil, err
	}
	return values.JSON(res), nil
}
