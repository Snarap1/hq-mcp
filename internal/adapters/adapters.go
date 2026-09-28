// Package adapters opens one fresh connection per tool call for a profile's
// adapter: postgres, mysql, mssql, odbc (compat), clickhouse, or redis.
package adapters

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/redis/go-redis/v9"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"

	"hq-mcp/internal/config"
)

// DB is an open connection to one profile. Connections are opened per tool
// call and closed afterwards, so the server can run next to a live database
// TUI session without sharing state.
type DB interface {
	// Kind is the adapter name: postgres, mysql, mssql, odbc, clickhouse, or redis.
	Kind() string
	// Close releases the connection.
	Close()
}

// SQL is a database/sql-backed connection.
type SQL struct {
	kind string
	db   *sql.DB
}

func (c *SQL) Kind() string { return c.kind }
func (c *SQL) Close()       { _ = c.db.Close() }

// SQL exposes the underlying handle; it is only valid for SQL adapters.
func (c *SQL) SQL() *sql.DB { return c.db }

// Redis is a go-redis connection.
type Redis struct {
	client *redis.Client
	sep    string
}

func (c *Redis) Kind() string { return "redis" }
func (c *Redis) Close()       { _ = c.client.Close() }

// Redis exposes the underlying client; it is only valid for redis profiles.
func (c *Redis) Redis() *redis.Client { return c.client }

// Sep is the key separator configured for the redis profile.
func (c *Redis) Sep() string { return c.sep }

// Open validates the profile and opens a fresh connection for its adapter.
func Open(p map[string]any) (DB, error) {
	adapter, err := config.Validate(p)
	if err != nil {
		return nil, err
	}
	switch adapter {
	case "postgres":
		return openPostgres(p)
	case "mysql":
		return openMySQL(p)
	case "mssql", "odbc":
		return openMSSQL(p, adapter)
	case "clickhouse":
		return openClickHouse(p)
	case "redis":
		return openRedis(p)
	default:
		return nil, fmt.Errorf("adapter %q is not supported; supported: %s", adapter, config.SupportedAdapters)
	}
}

// ErrNotSQL is the error for a tool that only works on SQL profiles, naming
// the redis equivalent when the profile is a redis one.
func ErrNotSQL(kind, tool string) error {
	if kind == "redis" {
		return fmt.Errorf("%s is not available for redis; use run_redis", tool)
	}
	return fmt.Errorf("%s is not available for %s", tool, kind)
}

// hostPort returns host and port with the given defaults; an empty host
// becomes localhost so DSNs never silently fall back to a unix socket.
func hostPort(p map[string]any, defaultHost string, defaultPort int) (string, int, error) {
	host, err := config.OptStr(p, "host")
	if err != nil {
		return "", 0, err
	}
	if host == "" {
		host = defaultHost
	}
	port, ok, err := config.OptInt(p, "port")
	if err != nil {
		return "", 0, err
	}
	if !ok || port == 0 {
		port = defaultPort
	}
	return host, port, nil
}

func openPostgres(p map[string]any) (DB, error) {
	p, err := config.FillFromURL(p, "postgres")
	if err != nil {
		return nil, err
	}
	host, port, err := hostPort(p, "localhost", 5432)
	if err != nil {
		return nil, err
	}
	user, err := config.OptStr(p, "user")
	if err != nil {
		return nil, err
	}
	pass, err := config.OptStr(p, "password")
	if err != nil {
		return nil, err
	}
	dbname, err := config.DBOpt(p)
	if err != nil {
		return nil, err
	}
	sslmode, err := config.OptStr(p, "sslmode")
	if err != nil {
		return nil, err
	}
	if sslmode == "" {
		sslmode = "prefer"
	}
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, pass, dbname, sslmode)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return &SQL{kind: "postgres", db: db}, nil
}

func openMySQL(p map[string]any) (DB, error) {
	p, err := config.FillFromURL(p, "mysql")
	if err != nil {
		return nil, err
	}
	host, port, err := hostPort(p, "localhost", 3306)
	if err != nil {
		return nil, err
	}
	user, err := config.OptStr(p, "user")
	if err != nil {
		return nil, err
	}
	pass, err := config.OptStr(p, "password")
	if err != nil {
		return nil, err
	}
	dbname, err := config.DBOpt(p)
	if err != nil {
		return nil, err
	}
	tlsOpt, err := config.OptStr(p, "tls")
	if err != nil {
		return nil, err
	}
	if tlsOpt == "" {
		tlsOpt = "false"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true&tls=%s",
		user, pass, net.JoinHostPort(host, strconv.Itoa(port)), dbname, tlsOpt)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	return &SQL{kind: "mysql", db: db}, nil
}

func openMSSQL(p map[string]any, adapter string) (DB, error) {
	var host string
	var port int
	var user, pass, dbname, encrypt string
	if adapter == "odbc" {
		cs, err := config.OptConnStr(p)
		if err != nil {
			return nil, err
		}
		if cs == "" {
			return nil, fmt.Errorf("adapter \"odbc\" requires conn_str")
		}
		opts, err := parseODBC(cs)
		if err != nil {
			return nil, err
		}
		host, port, user, pass, dbname, encrypt = opts.host, opts.port, opts.user, opts.password, opts.database, opts.encrypt
	} else {
		p, err := config.FillFromURL(p, "mssql")
		if err != nil {
			return nil, err
		}
		var err2 error
		host, port, err2 = hostPort(p, "localhost", 1433)
		if err2 != nil {
			return nil, err2
		}
		if user, err = config.OptStr(p, "user"); err != nil {
			return nil, err
		}
		if pass, err = config.OptStr(p, "password"); err != nil {
			return nil, err
		}
		if dbname, err = config.DBOpt(p); err != nil {
			return nil, err
		}
		if encrypt, err = config.OptStr(p, "encrypt"); err != nil {
			return nil, err
		}
	}
	if encrypt == "" {
		encrypt = "disable"
	}
	// The ODBC driver is not used; go-mssqldb speaks TDS directly.
	dsn := fmt.Sprintf("sqlserver://%s:%s@%s?database=%s&encrypt=%s",
		user, pass, net.JoinHostPort(host, strconv.Itoa(port)), dbname, encrypt)
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}
	return &SQL{kind: adapter, db: db}, nil
}

// odbcConn is the subset of an ODBC connection string hq-mcp understands.
type odbcConn struct {
	driver   string
	host     string
	port     int
	database string
	user     string
	password string
	encrypt  string
}

// parseODBC splits an ODBC connection string into its keys. Values may be
// braced ({ODBC Driver 18 for SQL Server}), which is how real profiles quote
// the driver name.
func parseODBC(cs string) (odbcConn, error) {
	var out odbcConn
	for _, field := range splitODBCFields(cs) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = unbraceODBCValue(strings.TrimSpace(value))
		switch key {
		case "driver":
			out.driver = value
		case "server", "data source", "addr", "address", "network address":
			host, portStr, hasPort := strings.Cut(value, ",")
			out.host = strings.TrimSpace(host)
			if hasPort {
				if strings.Contains(portStr, ",") {
					return out, fmt.Errorf("could not parse port from ODBC Server %q", value)
				}
				n, err := strconv.Atoi(strings.TrimSpace(portStr))
				if err != nil {
					return out, fmt.Errorf("could not parse port from ODBC Server %q: %v", value, err)
				}
				out.port = n
			}
		case "database", "initial catalog":
			out.database = value
		case "uid", "user", "username":
			out.user = value
		case "pwd", "password":
			out.password = value
		case "encrypt":
			out.encrypt = mapEncrypt(value)
		}
	}
	if out.host == "" {
		return out, fmt.Errorf("ODBC conn_str has no Server: %s", cs)
	}
	if out.port == 0 {
		out.port = 1433
	}
	return out, nil
}

// splitODBCFields splits on ';' while keeping braced values intact.
func splitODBCFields(cs string) []string {
	var fields []string
	var cur strings.Builder
	braced := false
	for _, r := range cs {
		switch {
		case r == '{':
			braced = true
			cur.WriteRune(r)
		case r == '}':
			braced = false
			cur.WriteRune(r)
		case r == ';' && !braced:
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	return fields
}

func unbraceODBCValue(v string) string {
	if len(v) >= 2 && strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
		return v[1 : len(v)-1]
	}
	return v
}

// mapEncrypt translates the ODBC Encrypt values into go-mssqldb encrypt values.
func mapEncrypt(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "no", "false", "0", "optional":
		return "disable"
	case "strict":
		return "strict"
	default:
		return "true"
	}
}

// defaultClickHouseProtocol is HTTP for the HTTP ports and native otherwise.
func defaultClickHouseProtocol(port int) clickhouse.Protocol {
	if port == 8123 || port == 8443 {
		return clickhouse.HTTP
	}
	return clickhouse.Native
}

// clickhouseProtocol maps a profile protocol value to the driver protocol.
func clickhouseProtocol(name string) (clickhouse.Protocol, error) {
	switch name {
	case "http", "https":
		return clickhouse.HTTP, nil
	case "native":
		return clickhouse.Native, nil
	}
	return 0, fmt.Errorf("clickhouse protocol must be \"http\" or \"native\" (got %q)", name)
}

func openClickHouse(p map[string]any) (DB, error) {
	p, err := config.FillFromURL(p, "clickhouse")
	if err != nil {
		return nil, err
	}
	host, port, err := hostPort(p, "localhost", 8123)
	if err != nil {
		return nil, err
	}
	user, err := config.OptStr(p, "user")
	if err != nil {
		return nil, err
	}
	pass, err := config.OptStr(p, "password")
	if err != nil {
		return nil, err
	}
	database, err := config.OptStr(p, "database")
	if err != nil {
		return nil, err
	}
	if database == "" {
		database, err = config.DBOpt(p)
		if err != nil {
			return nil, err
		}
	}
	secure, err := config.OptBool(p, "secure")
	if err != nil {
		return nil, err
	}
	protocol, err := config.OptStr(p, "protocol")
	if err != nil {
		return nil, err
	}
	opts := &clickhouse.Options{
		Addr: []string{net.JoinHostPort(host, strconv.Itoa(port))},
		Auth: clickhouse.Auth{Database: database, Username: user, Password: pass},
	}
	if protocol == "" {
		opts.Protocol = defaultClickHouseProtocol(port)
	} else {
		proto, err := clickhouseProtocol(protocol)
		if err != nil {
			return nil, err
		}
		opts.Protocol = proto
	}
	if secure {
		opts.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	db := clickhouse.OpenDB(opts)
	return &SQL{kind: "clickhouse", db: db}, nil
}

func openRedis(p map[string]any) (DB, error) {
	host, port, err := hostPort(p, "localhost", 6379)
	if err != nil {
		return nil, err
	}
	user, err := config.OptStr(p, "user")
	if err != nil {
		return nil, err
	}
	pass, err := config.OptStr(p, "password")
	if err != nil {
		return nil, err
	}
	dbNum, _, err := config.OptInt(p, "database")
	if err != nil {
		return nil, err
	}
	secure, err := config.OptBool(p, "secure")
	if err != nil {
		return nil, err
	}
	sep, err := config.OptStr(p, "separator")
	if err != nil {
		return nil, err
	}
	if sep == "" {
		sep = ":"
	}
	opts := &redis.Options{
		Addr:     net.JoinHostPort(host, strconv.Itoa(port)),
		Username: user,
		Password: pass,
		DB:       dbNum,
	}
	if secure {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	return &Redis{client: redis.NewClient(opts), sep: sep}, nil
}
