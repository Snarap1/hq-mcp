#!/usr/bin/env bash
# Bring up the local Docker fixtures hq-mcp's e2e tests use.
# Everything binds to 127.0.0.1 on non-default ports; no real hosts or
# credentials are involved.
#
#   bash docs/plans/fixtures.sh up
#   HQ_MCP_E2E=1 go test -run TestE2E -v .
#   bash docs/plans/fixtures.sh down
set -euo pipefail

PG_NAME=hq-pg-dev
MY_NAME=hq-my-dev
MS_NAME=hq-ms-dev
CH_NAME=hq-ch-dev
REDIS_NAME=hq-redis-dev

PG_PORT=55432
MY_PORT=53306
MS_PORT=51433
CH_HTTP_PORT=58123
CH_NATIVE_PORT=59000
REDIS_PORT=56379

log() { printf '%s fixtures: %s\n' "$(date +%H:%M:%S)" "$*" >&2; }

wait_for() {
  local name=$1 tries=$2
  shift 2
  local i
  for ((i = 0; i < tries; i++)); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  log "container $name did not become ready in time"
  docker logs --tail 30 "$name" >&2 || true
  return 1
}

remove_all() {
  docker rm -f "$PG_NAME" "$MY_NAME" "$MS_NAME" "$CH_NAME" "$REDIS_NAME" >/dev/null 2>&1 || true
}

up() {
  remove_all

  log "starting postgres on 127.0.0.1:$PG_PORT"
  docker run -d --name "$PG_NAME" \
    -e POSTGRES_USER=hq -e POSTGRES_PASSWORD=hq -e POSTGRES_DB=hqdb \
    -p "127.0.0.1:$PG_PORT:5432" postgres:16-alpine >/dev/null
  wait_for "$PG_NAME" 60 docker exec "$PG_NAME" pg_isready -U hq -d hqdb

  log "starting mysql on 127.0.0.1:$MY_PORT"
  docker run -d --name "$MY_NAME" \
    -e MYSQL_ROOT_PASSWORD=rootpw -e MYSQL_USER=hq -e MYSQL_PASSWORD=hq -e MYSQL_DATABASE=hqdb \
    -p "127.0.0.1:$MY_PORT:3306" mysql:8 >/dev/null
  wait_for "$MY_NAME" 120 docker exec "$MY_NAME" mysqladmin ping -uroot -prootpw

  log "starting mssql on 127.0.0.1:$MS_PORT"
  docker run -d --name "$MS_NAME" \
    -e ACCEPT_EULA=Y -e MSSQL_SA_PASSWORD='Hq!Passw0rd' \
    -p "127.0.0.1:$MS_PORT:1433" mcr.microsoft.com/mssql/server:2022-latest >/dev/null
  wait_for "$MS_NAME" 120 docker exec "$MS_NAME" /opt/mssql-tools18/bin/sqlcmd \
    -S localhost -U sa -P 'Hq!Passw0rd' -C -Q 'SELECT 1'

  log "starting clickhouse on 127.0.0.1:$CH_HTTP_PORT (http) and $CH_NATIVE_PORT (native)"
  docker run -d --name "$CH_NAME" \
    -e CLICKHOUSE_USER=hq -e CLICKHOUSE_PASSWORD=hq -e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
    -p "127.0.0.1:$CH_HTTP_PORT:8123" -p "127.0.0.1:$CH_NATIVE_PORT:9000" \
    clickhouse/clickhouse-server:24-alpine >/dev/null
  wait_for "$CH_NAME" 90 docker exec "$CH_NAME" clickhouse-client --user hq --password hq -q 'SELECT 1'

  log "starting redis on 127.0.0.1:$REDIS_PORT"
  docker run -d --name "$REDIS_NAME" -p "127.0.0.1:$REDIS_PORT:6379" redis:7-alpine >/dev/null
  wait_for "$REDIS_NAME" 30 docker exec "$REDIS_NAME" redis-cli ping

  log "seeding data"
  docker exec -i "$PG_NAME" psql -U hq -d hqdb -v ON_ERROR_STOP=1 -q <<'SQL'
CREATE TABLE public.widgets(id serial primary key, name text, price numeric);
INSERT INTO public.widgets(name, price) VALUES ('bolt', 1.5), ('nut', 2.5), ('screw', 0.5);
CREATE SCHEMA analytics;
CREATE TABLE analytics.widgets_extra(id serial primary key, label text);
INSERT INTO analytics.widgets_extra(label) VALUES ('extra');
SQL
  docker exec "$MY_NAME" mysql -uroot -prootpw -e "
    CREATE DATABASE hqdb2;
    CREATE TABLE hqdb.widgets(id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(64), price DECIMAL(10,2));
    INSERT INTO hqdb.widgets(name, price) VALUES ('bolt', 1.50), ('nut', 2.50);
    CREATE TABLE hqdb2.widgets(id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(64));
    GRANT ALL PRIVILEGES ON hqdb.* TO 'hq'@'%';
    GRANT ALL PRIVILEGES ON hqdb2.* TO 'hq'@'%';
    FLUSH PRIVILEGES;
  "
  docker exec "$MS_NAME" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'Hq!Passw0rd' -C -b -Q "CREATE DATABASE hqdb"
  ms() { docker exec "$MS_NAME" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P 'Hq!Passw0rd' -C -b -d hqdb -Q "$1"; }
  ms "CREATE TABLE dbo.widgets(id INT IDENTITY PRIMARY KEY, name VARCHAR(64), price DECIMAL(10,2))"
  ms "INSERT INTO dbo.widgets(name, price) VALUES ('bolt', 1.50), ('nut', 2.50)"
  ms "CREATE SCHEMA analytics"
  ms "CREATE TABLE analytics.widgets_extra(id INT PRIMARY KEY, label VARCHAR(64))"

  docker exec "$CH_NAME" clickhouse-client --user hq --password hq -q "
    CREATE DATABASE IF NOT EXISTS hqdb;
    CREATE TABLE IF NOT EXISTS hqdb.widgets(id UInt32, name String, note Nullable(String), price Decimal(10,2))
      ENGINE = MergeTree ORDER BY id;
    INSERT INTO hqdb.widgets VALUES (1, 'bolt', 'a bolt', 1.50), (2, 'nut', NULL, 2.50);
  "
  docker exec "$REDIS_NAME" redis-cli SET 'user:1000' '{"id":1000}'
  docker exec "$REDIS_NAME" redis-cli SADD 'user:1000:roles' admin viewer
  docker exec "$REDIS_NAME" redis-cli SET plainkey 1
  docker exec "$REDIS_NAME" redis-cli LPUSH 'queue:a' a b c

  log "fixtures are up"
}

down() {
  remove_all
  log "fixtures are down"
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  *)
    printf 'usage: %s up|down\n' "$0" >&2
    exit 2
    ;;
esac
