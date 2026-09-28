package rediscmd

import "testing"

func TestCommandViolation(t *testing.T) {
	allowed := []string{
		"GET user:1000", "get user:1000", "SCAN 0 MATCH user:* COUNT 100",
		"HGETALL user:1000", "SMEMBERS user:1000:roles", "TTL user:1000",
		"INFO", "PING", "ZRANGE z 0 -1", "XINFO STREAM s", "GEOSEARCH g FROMMEMBER m BYRADIUS 1 m ASC",
		"SET k v", "set k v EX 60", "SET k v NX KEEPTTL", "set k v XX PX 1000",
	}
	for _, cmd := range allowed {
		if v := CommandViolation(cmd); v != "" {
			t.Errorf("CommandViolation(%q) = %q, want allowed", cmd, v)
		}
	}
	refused := []string{
		"DEL user:1000", "EXPIRE k 10", "EVAL \"return 1\" 0", "MSET a 1 b 2",
		"SETNX k v", "GETSET k v", "INCR k", "HSET h f v", "LPUSH l v", "SADD s v",
		"CONFIG SET maxmemory 1mb", "SUBSCRIBE ch", "MONITOR", "FLUSHALL",
		"SHUTDOWN NOSAVE", "BLPOP q 0", "AUTH pw", "SELECT 1", "MULTI",
		"", "   ",
	}
	for _, cmd := range refused {
		if v := CommandViolation(cmd); v == "" {
			t.Errorf("CommandViolation(%q) = \"\", want refused", cmd)
		}
	}
}

func TestRefusalMessage(t *testing.T) {
	got := CommandViolation("DEL user:1000")
	want := `command "DEL" is not allowed; this server permits read commands and SET only`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEmptyCommandRefused(t *testing.T) {
	if got := CommandViolation("   "); got != "empty command" {
		t.Errorf("got %q, want %q", got, "empty command")
	}
}
