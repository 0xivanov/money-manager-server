#!/bin/sh

set -eu

: "${DATABASE_URL:?set DATABASE_URL to the production PostgreSQL connection string}"
: "${BACKUP_CERT_PATH:?set BACKUP_CERT_PATH to the public X.509 backup certificate}"

namespace=${BACKUP_NAMESPACE:-deployer-apps}
nodes=${BACKUP_NODES:-"pi-home pi-yasen-2"}
image=${BACKUP_IMAGE:-"ghcr.io/0xivanov/money-manager-backup@sha256:b668d1ffdbdb8fbfb13b714fda0ba4bee0db8ba86518ada9fceacc634f910d93"}
retention_days=${BACKUP_RETENTION_DAYS:-30}

test -r "$BACKUP_CERT_PATH"

kubectl_run() {
    if command -v kubectl >/dev/null 2>&1; then
        kubectl "$@"
    elif command -v k3s >/dev/null 2>&1; then
        k3s kubectl "$@"
    else
        echo "kubectl or k3s is required" >&2
        return 127
    fi
}

kubectl_run -n "$namespace" create secret generic money-manager-backup-database \
    --from-literal=DATABASE_URL="$DATABASE_URL" \
    --dry-run=client \
    --output=yaml | kubectl_run apply -f -

kubectl_run -n "$namespace" create configmap money-manager-backup-certificate \
    --from-file=backup-cert.pem="$BACKUP_CERT_PATH" \
    --dry-run=client \
    --output=yaml | kubectl_run apply -f -

minute=37
for node in $nodes; do
    job_name=$(printf '%s' "$node" | tr '_' '-' | tr -cd 'a-zA-Z0-9-')
    cat <<EOF | kubectl_run apply -f -
apiVersion: batch/v1
kind: CronJob
metadata:
  name: money-manager-backup-${job_name}
  namespace: ${namespace}
  labels:
    app.kubernetes.io/name: money-manager-backup
spec:
  schedule: "${minute} 2 * * *"
  timeZone: "Etc/UTC"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      backoffLimit: 2
      activeDeadlineSeconds: 900
      ttlSecondsAfterFinished: 604800
      template:
        metadata:
          labels:
            app.kubernetes.io/name: money-manager-backup
        spec:
          restartPolicy: Never
          nodeSelector:
            kubernetes.io/hostname: ${node}
          securityContext:
            seccompProfile:
              type: RuntimeDefault
          containers:
            - name: backup
              image: ${image}
              imagePullPolicy: IfNotPresent
              command:
                - /bin/sh
                - -ec
                - |
                  umask 077
                  timestamp=\$(date -u +%Y%m%dT%H%M%SZ)
                  temporary=/tmp/money-manager-\${timestamp}.dump
                  encrypted_temporary=/backups/.money-manager-\${timestamp}.dump.cms.tmp
                  destination=/backups/money-manager-\${timestamp}.dump.cms
                  cleanup() {
                    rm -f "\$temporary" "\$encrypted_temporary"
                  }
                  trap cleanup EXIT
                  trap 'exit 1' HUP INT TERM
                  test ! -e "\$destination"
                  pg_dump --format=custom --compress=9 --file "\$temporary" "\$DATABASE_URL"
                  pg_restore --list "\$temporary" >/dev/null
                  openssl cms -encrypt -binary -aes-256-cbc \
                    -in "\$temporary" \
                    -outform DER \
                    -out "\$encrypted_temporary" \
                    /etc/money-manager-backup/backup-cert.pem
                  openssl cms -cmsout -inform DER -in "\$encrypted_temporary" -print >/dev/null
                  mv "\$encrypted_temporary" "\$destination"
                  find /backups -type f -name 'money-manager-*.dump.cms' -mtime +${retention_days} -delete
              envFrom:
                - secretRef:
                    name: money-manager-backup-database
              resources:
                requests:
                  cpu: 25m
                  memory: 64Mi
                limits:
                  cpu: 500m
                  memory: 256Mi
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop: ["ALL"]
                readOnlyRootFilesystem: true
                runAsNonRoot: false
                runAsUser: 0
              volumeMounts:
                - name: backups
                  mountPath: /backups
                - name: certificate
                  mountPath: /etc/money-manager-backup
                  readOnly: true
                - name: temporary
                  mountPath: /tmp
          volumes:
            - name: backups
              hostPath:
                path: /var/lib/money-manager-backups
                type: DirectoryOrCreate
            - name: certificate
              configMap:
                name: money-manager-backup-certificate
            - name: temporary
              emptyDir:
                sizeLimit: 512Mi
EOF
    minute=$((minute + 8))
done

echo "Encrypted daily backups are scheduled on: $nodes"
