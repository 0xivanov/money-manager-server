# Encrypted off-host backups

The production PostgreSQL server keeps a short local dump history. This folder adds encrypted daily dumps on each Raspberry Pi, so loss of the VPS disk does not remove every copy.

`install-offsite-backups.sh` creates one node-pinned Kubernetes CronJob per Pi. Each job validates a custom-format dump before encrypting it with an X.509 public certificate. The corresponding private key must remain outside the cluster and outside Git.

Build and publish the ARM64 backup image, then pass its immutable digest through `BACKUP_IMAGE`. Install with a kubeconfig that can write to `deployer-apps`:

```sh
DATABASE_URL='postgres://...' \
BACKUP_CERT_PATH="$HOME/.config/money-manager/backup-certificate.pem" \
BACKUP_IMAGE='ghcr.io/0xivanov/money-manager-backup@sha256:...' \
./ops/backup/install-offsite-backups.sh
```

After installation, create an immediate job from each CronJob and inspect its logs. A restore drill must copy one `.cms` archive off a Pi, decrypt it with `openssl cms -decrypt`, validate it with `pg_restore --list`, restore it into a temporary database, query the restored tables, and delete the temporary database.
