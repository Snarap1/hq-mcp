package adapters

import (
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestParseODBC(t *testing.T) {
	cases := []struct {
		name string
		conn string
		want odbcConn
	}{
		{
			name: "mssql driver string",
			conn: "Driver={ODBC Driver 18 for SQL Server};Server=127.0.0.1,1433;Database=dev_db;Uid=sa;Pwd=pw;Encrypt=no;",
			want: odbcConn{
				driver:   "ODBC Driver 18 for SQL Server",
				host:     "127.0.0.1",
				port:     1433,
				database: "dev_db",
				user:     "sa",
				password: "pw",
				encrypt:  "disable",
			},
		},
		{
			name: "server without port defaults to 1433",
			conn: "Server=127.0.0.1;Database=dev_db;",
			want: odbcConn{host: "127.0.0.1", port: 1433, database: "dev_db"},
		},
		{
			name: "encrypt mandatory maps to true",
			conn: "Server=127.0.0.1;Encrypt=yes;",
			want: odbcConn{host: "127.0.0.1", port: 1433, encrypt: "true"},
		},
		{
			name: "encrypt strict stays strict",
			conn: "Server=127.0.0.1;Encrypt=strict;",
			want: odbcConn{host: "127.0.0.1", port: 1433, encrypt: "strict"},
		},
		{
			name: "braced value keeps semicolons out of the split",
			conn: "Driver={ODBC;Driver 18};Server=127.0.0.1;",
			want: odbcConn{driver: "ODBC;Driver 18", host: "127.0.0.1", port: 1433},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseODBC(tc.conn)
			if err != nil {
				t.Fatalf("parseODBC(%q): %v", tc.conn, err)
			}
			if got != tc.want {
				t.Errorf("parseODBC(%q) = %+v, want %+v", tc.conn, got, tc.want)
			}
		})
	}
}

func TestParseODBCMissingServer(t *testing.T) {
	if _, err := parseODBC("Driver={X};Database=db;"); err == nil {
		t.Error("expected an error when the ODBC string has no Server")
	}
}

func TestClickHouseProtocolDefault(t *testing.T) {
	cases := []struct {
		port int
		want clickhouse.Protocol
	}{
		{8123, clickhouse.HTTP},
		{8443, clickhouse.HTTP},
		{9000, clickhouse.Native},
		{59000, clickhouse.Native},
	}
	for _, tc := range cases {
		if got := defaultClickHouseProtocol(tc.port); got != tc.want {
			t.Errorf("port %d: protocol = %v, want %v", tc.port, got, tc.want)
		}
	}
}

func TestErrNotSQLNamesRedisEquivalent(t *testing.T) {
	if got := ErrNotSQL("redis", "run_query").Error(); got != "run_query is not available for redis; use run_redis" {
		t.Errorf("got %q", got)
	}
	if got := ErrNotSQL("duckdb", "run_query").Error(); got != "run_query is not available for duckdb" {
		t.Errorf("got %q", got)
	}
}

func TestOpenRejectsUnknownProfileKey(t *testing.T) {
	// Open validates before touching any driver.
	_, err := Open(map[string]any{"adapter": "postgres", "nope": "x"})
	if err == nil {
		t.Fatal("expected an error for an unknown profile key")
	}
	want := `unknown option "nope" for adapter "postgres"; valid options: adapter, conn_str, database, dbname, host, password, port, sslmode, user`
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}
