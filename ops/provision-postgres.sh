#!/bin/sh

set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo "run this script as root" >&2
    exit 1
fi

database_name="money_manager"
database_user="money_manager"
wireguard_address="10.8.0.1"
secret_dir="/etc/money-manager"
backup_dir="/var/backups/money-manager"

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends postgresql-17 openssl ca-certificates

install -d -m 0700 -o root -g root "$secret_dir"
if [ ! -s "$secret_dir/postgres-password" ]; then
    umask 077
    openssl rand -hex 32 > "$secret_dir/postgres-password"
fi
if [ ! -s "$secret_dir/jwt-secret" ]; then
    umask 077
    openssl rand -hex 48 > "$secret_dir/jwt-secret"
fi
chmod 0600 "$secret_dir/postgres-password" "$secret_dir/jwt-secret"

postgres_password=$(tr -d '\r\n' < "$secret_dir/postgres-password")
postgres_config="/etc/postgresql/17/main/conf.d/money-manager.conf"
postgres_hba="/etc/postgresql/17/main/pg_hba.conf"

install -m 0644 -o postgres -g postgres /dev/stdin "$postgres_config" <<EOF
# Managed by money-manager-server/ops/provision-postgres.sh
listen_addresses = '127.0.0.1,${wireguard_address}'
port = 5432
ssl = on
password_encryption = 'scram-sha-256'
max_connections = 60
shared_buffers = '256MB'
effective_cache_size = '1GB'
maintenance_work_mem = '64MB'
idle_in_transaction_session_timeout = '60s'
statement_timeout = '30s'
log_min_duration_statement = '1000ms'
log_line_prefix = '%m [%p] %q%u@%d '
EOF

if ! grep -q '^# money-manager access$' "$postgres_hba"; then
    cp -a "$postgres_hba" "$postgres_hba.pre-money-manager"
    install -m 0600 -o postgres -g postgres /dev/stdin /tmp/money-manager-hba <<EOF
# money-manager access
hostssl ${database_name} ${database_user} 10.42.0.0/16 scram-sha-256
hostssl ${database_name} ${database_user} 10.8.0.0/24 scram-sha-256
EOF
    sed -i '1r /tmp/money-manager-hba' "$postgres_hba"
    rm -f /tmp/money-manager-hba
fi

systemctl enable --now postgresql
systemctl restart postgresql

if ! runuser -u postgres -- psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='${database_user}'" | grep -q 1; then
    runuser -u postgres -- psql -v role_password="$postgres_password" <<'SQL'
CREATE ROLE money_manager LOGIN PASSWORD :'role_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
SQL
else
    runuser -u postgres -- psql -v role_password="$postgres_password" <<'SQL'
ALTER ROLE money_manager PASSWORD :'role_password';
SQL
fi

if ! runuser -u postgres -- psql -tAc "SELECT 1 FROM pg_database WHERE datname='${database_name}'" | grep -q 1; then
    runuser -u postgres -- createdb --owner "$database_user" "$database_name"
fi

runuser -u postgres -- psql -d "$database_name" <<'SQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO money_manager;
SQL

install -d -m 0700 -o postgres -g postgres "$backup_dir"
install -m 0750 -o root -g postgres /dev/stdin /usr/local/sbin/money-manager-db-backup <<'EOF'
#!/bin/sh
set -eu
umask 077

backup_dir="/var/backups/money-manager"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
temporary="$backup_dir/.money-manager-$timestamp.dump"
destination="$backup_dir/money-manager-$timestamp.dump"

cleanup() {
    rm -f "$temporary"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

pg_dump --format=custom --compress=9 --file "$temporary" money_manager
pg_restore --list "$temporary" >/dev/null
mv "$temporary" "$destination"
find "$backup_dir" -type f -name 'money-manager-*.dump' -mtime +14 -delete
EOF

install -m 0644 -o root -g root /dev/stdin /etc/systemd/system/money-manager-db-backup.service <<'EOF'
[Unit]
Description=Back up the Money Manager PostgreSQL database
After=postgresql.service
Requires=postgresql.service

[Service]
Type=oneshot
User=postgres
Group=postgres
ExecStart=/usr/local/sbin/money-manager-db-backup
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/var/backups/money-manager
EOF

install -m 0644 -o root -g root /dev/stdin /etc/systemd/system/money-manager-db-backup.timer <<'EOF'
[Unit]
Description=Daily Money Manager database backup

[Timer]
OnCalendar=*-*-* 02:15:00 UTC
Persistent=true
RandomizedDelaySec=15m
Unit=money-manager-db-backup.service

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now money-manager-db-backup.timer
systemctl start money-manager-db-backup.service

runuser -u postgres -- pg_isready -h "$wireguard_address" -d "$database_name"
latest_backup=$(find "$backup_dir" -type f -name 'money-manager-*.dump' -printf '%T@ %p\n' | sort -nr | head -n 1 | cut -d' ' -f2-)
test -n "$latest_backup"
runuser -u postgres -- pg_restore --list "$latest_backup" >/dev/null

echo "PostgreSQL is ready on the private WireGuard address; backup and restore-list checks passed."
