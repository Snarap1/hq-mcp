# Adapter profile keys

Every profile is a TOML table under `[profiles.<name>]` with one required key, `adapter`.
Any other key outside the adapter's list is rejected with the valid-option list, so copy the matching table rather than mixing keys between adapters.

`database` is an alias of `dbname` for `postgres`, `mysql`, and `mssql`; `dbname` wins when both are present.
`conn_str` is accepted by every adapter except `redis`.

## postgres

Keys: `adapter`, `conn_str`, `database`, `dbname`, `host`, `password`, `port`, `sslmode`, `user`.

Defaults: host `localhost`, port `5432`, `sslmode = "prefer"`.
An empty host becomes `localhost` rather than a unix socket.

```toml
[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"
```

## mysql

Keys: `adapter`, `conn_str`, `database`, `dbname`, `host`, `password`, `port`, `tls`, `user`.

Defaults: host `localhost`, port `3306`, `tls = "false"`.
The `tls` value is passed to the MySQL driver as its `tls` parameter, so use the driver's registered names (`true`, `skip-verify`, `preferred`) and register a custom one in code if needed.

```toml
[profiles.local-my]
adapter = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
password = "app"
dbname = "appdb"
```

## mssql

Keys: `adapter`, `conn_str`, `database`, `dbname`, `encrypt`, `host`, `password`, `port`, `user`.

Defaults: host `localhost`, port `1433`, `encrypt = "disable"`.

```toml
[profiles.local-ms]
adapter = "mssql"
host = "127.0.0.1"
port = 1433
user = "sa"
password = "app"
dbname = "appdb"
encrypt = "disable"
```

## odbc

Keys: `adapter`, `conn_str` only.
`conn_str` is required; without it the server returns `adapter "odbc" requires conn_str`.

The ODBC driver is never loaded: the server parses the string itself and connects over TDS with go-mssqldb.
Recognized keys, case-insensitive: `Driver` (read but unused), `Server` / `Data Source` / `Addr` / `Address` / `Network Address` (`host[,port]`), `Database` / `Initial Catalog`, `Uid` / `User` / `UserName`, `Pwd` / `Password`, `Encrypt`.
A missing port becomes `1433`; a missing `Server` is the error `ODBC conn_str has no Server: <conn_str>`.

`Encrypt` maps as: `no`, `false`, `0`, `optional` to `disable`; `strict` to `strict`; anything else, including `yes` and `true`, to `true`.

`conn_str` may be a string or a list of strings, joined with a single space.
Real profiles use the list form, and each element ends with `;`:

```toml
[profiles.local-odbc]
adapter = "odbc"
conn_str = [
  "Driver={ODBC Driver 18 for SQL Server};",
  "Server=127.0.0.1,1433;",
  "Database=appdb;",
  "Uid=sa;",
  "Pwd=app;",
  "Encrypt=no;",
]
```

## clickhouse

Keys: `adapter`, `conn_str`, `database`, `host`, `password`, `port`, `protocol`, `secure`, `user`.

Defaults: host `localhost`, port `8123`.
The database key is `database` here; `dbname` is accepted as a fallback when `database` is empty.
`protocol` is `http`, `https`, or `native`; anything else fails with `clickhouse protocol must be "http" or "native" (got "...")`.
When `protocol` is absent the port decides: `8123` and `8443` mean HTTP, everything else means native.
Set `protocol = "http"` explicitly whenever a container or tunnel maps some other host port onto 8123, because the port heuristic only recognizes those two numbers.
`secure = true` turns on TLS with a minimum of TLS 1.2.

```toml
[profiles.local-ch]
adapter = "clickhouse"
host = "127.0.0.1"
port = 8123
protocol = "http"
user = "app"
password = "app"
database = "appdb"
```

## redis

Keys: `adapter`, `database`, `host`, `password`, `port`, `secure`, `separator`, `user`.
There is no `conn_str`: a redis profile is host and port only.

Defaults: host `localhost`, port `6379`, database `0`, `separator = ":"`.
`database` is the redis DB index as an integer, not a name.
`secure = true` turns on TLS with a minimum of TLS 1.2.
`separator` is what `get_schema` splits key names on; set it to match the key convention of the instance (`:` for `user:1:name`, `-` or `.` for other layouts).

```toml
[profiles.local-redis]
adapter = "redis"
host = "127.0.0.1"
port = 6379
database = 0
separator = ":"
```

## URL-form conn_str

For `postgres`, `mysql`, `mssql`, and `clickhouse`, `conn_str` may be a URL whose scheme is `postgres://`, `mysql://`, `sqlserver://`, `http://`, `https://`, or `clickhouse://`.
The path sets the database (`database` for clickhouse, `dbname` otherwise), the authority sets `host`, `port`, `user`, and `password`.

URL parts only fill keys that are absent from the profile: explicit keys always win.

```toml
[profiles.url-pg]
adapter = "postgres"
conn_str = "postgres://app:app@127.0.0.1:5432/appdb?sslmode=require"
sslmode = "verify-full"
```

## Worked example: one config with every adapter

```toml
default_profile = "local-pg"

[profiles.local-pg]
adapter = "postgres"
host = "127.0.0.1"
port = 5432
user = "app"
password = "app"
dbname = "appdb"

[profiles.local-my]
adapter = "mysql"
host = "127.0.0.1"
port = 3306
user = "app"
password = "app"
dbname = "appdb"

[profiles.local-ms]
adapter = "mssql"
host = "127.0.0.1"
port = 1433
user = "sa"
password = "app"
dbname = "appdb"

[profiles.local-odbc]
adapter = "odbc"
conn_str = [
  "Driver={ODBC Driver 18 for SQL Server};",
  "Server=127.0.0.1,1433;",
  "Database=appdb;",
  "Uid=sa;",
  "Pwd=app;",
  "Encrypt=no;",
]

[profiles.local-ch]
adapter = "clickhouse"
host = "127.0.0.1"
port = 8123
protocol = "http"
user = "app"
password = "app"
database = "appdb"

[profiles.local-redis]
adapter = "redis"
host = "127.0.0.1"
port = 6379
database = 0
separator = ":"
```
