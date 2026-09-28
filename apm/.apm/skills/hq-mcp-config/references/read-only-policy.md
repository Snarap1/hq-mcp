# Read-only policy

The config decides where the server connects; this policy decides what it may run once connected.
A user reporting "the query was refused" is almost always hitting this, not a broken profile.

## SQL screening

`run_query` and `export_query` screen every statement before it reaches the database.
Screening is defense in depth, applied to the statement with comments and literals stripped first.

1. The statement must open with one of: `SELECT`, `WITH`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `DESC`, `TABLE`, `INSERT`, `UPDATE`.
   Anything else is refused with `statement is not allowed; only SELECT / WITH / EXPLAIN / SHOW / DESCRIBE / TABLE / INSERT / UPDATE queries are allowed`.
2. One statement per call.
   A `;` separating two statements is refused with `multiple statements are not allowed; send one read-only query`.
3. No write keyword may appear anywhere in the remaining text, refused with `statement contains write keyword(s): <sorted list>`:
   `delete`, `drop`, `truncate`, `alter`, `create`, `replace`, `merge`, `upsert`, `grant`, `revoke`, `vacuum`, `attach`, `detach`, `copy`, `call`, `do`, `lock`, `rename`, `reindex`, `refresh`, `comment`, `nextval`, `setval`.

Stripping happens before the checks, so text inside `-- comments`, `/* blocks */`, `'literals'`, `"quoted identifiers"`, and `$$ dollar-quoted $$` bodies cannot trigger or satisfy a keyword.

Consequences worth stating up front rather than after the user hits them:

- `INSERT` and `UPDATE` are allowed on purpose, for parity with the Python server this replaced.
- `INSERT ... ON CONFLICT DO UPDATE` is refused, because `do` is a write keyword. Do not "fix" this for a user; it is intentional.
- A data-modifying CTE (`WITH x AS (DELETE ...) SELECT ...`) is refused for the same reason.
- Column or table names that happen to contain a keyword as a whole word, such as a column named `comment`, are refused too. Quote-insensitive: renaming the column in the query is the only workaround.

The SQL is sent to the database unmodified.
No `LIMIT` is injected, so bound the response with the `limit` argument; `run_query` defaults to 50 rows and `export_query` to unlimited when `limit` is 0.

## Redis screening

`run_redis` is default-deny: the leading word of `command` must be on the read-only allowlist, case-insensitively, or the call is refused with `command "<cmd>" is not allowed; this server permits read-only Redis commands only`.
An empty command is refused with `empty command`.

Allowed: `ping`, `time`, `info`, `dbsize`, `scan`, `keys`, `type`, `exists`, `ttl`, `pttl`, `strlen`, `get`, `getrange`, `getbit`, `mget`, `bitcount`, `lpos`, `hget`, `hgetall`, `hkeys`, `hlen`, `hmget`, `hvals`, `hscan`, `lrange`, `lindex`, `llen`, `scard`, `smembers`, `sismember`, `smismember`, `srandmember`, `sscan`, `zcard`, `zcount`, `zlexcount`, `zrange`, `zrangebyscore`, `zrangebylex`, `zrevrange`, `zrank`, `zrevrank`, `zscore`, `zmscore`, `zscan`, `xlen`, `xrange`, `xrevrange`, `xinfo`, `object`, `memory`, `randomkey`, `geopos`, `geodist`, `geohash`, `geosearch`.

Refused by omission: every write, all blocking commands, `eval` and `evalsha`, `subscribe` and `monitor`, `config`, `select`, `flush*`, and `shutdown`.

Inline arguments in `command` are split on whitespace, so quoting is not honoured: `GET "user:1000"` looks up a key that includes literal quote characters.
Put arguments with spaces in the `args` array instead, which is passed through verbatim.

## Cross-adapter guardrails

`run_query` and `export_query` on a redis profile return `<tool> is not available for redis; use run_redis`; `run_redis` on a SQL profile returns `run_redis is not available for <adapter>`.
`get_columns` on a redis profile returns `get_columns is not available for redis; use get_schema`; use `get_schema` with `include_columns` to sample key types there.
