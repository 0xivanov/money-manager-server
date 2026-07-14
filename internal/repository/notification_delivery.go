package repository

import (
	"context"
	"encoding/json"
	"time"
)

type NotificationDelivery struct {
	ID             int
	NotificationID int
	DeviceID       int
	Attempts       int
	Platform       string
	DeviceToken    string
	AppID          string
	Environment    string
	EventType      string
	Title          string
	Body           string
	Payload        json.RawMessage
}

func (r *Repository) ClaimNotificationDeliveries(
	ctx context.Context,
	now time.Time,
	staleBefore time.Time,
	expiredBefore time.Time,
	platforms []string,
	limit int,
) ([]NotificationDelivery, error) {
	if len(platforms) == 0 || limit < 1 {
		return []NotificationDelivery{}, nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE notification_deliveries delivery
		SET status='pending',locked_at=NULL,available_at=$1,updated_at=now()
		FROM push_devices device
		WHERE device.id=delivery.device_id AND device.platform=ANY($2)
		  AND delivery.status='processing' AND delivery.locked_at < $3`, now, platforms, staleBefore); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE notification_deliveries delivery
		SET status='dead',locked_at=NULL,last_error='notification expired before delivery',updated_at=now()
		FROM notification_outbox notification
		WHERE notification.id=delivery.notification_id AND delivery.status='pending'
		  AND notification.created_at < $1`, expiredBefore); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE notification_outbox notification SET
		status=CASE WHEN EXISTS(SELECT 1 FROM notification_deliveries delivery
			WHERE delivery.notification_id=notification.id AND delivery.status='sent')
			THEN 'sent' ELSE 'dead' END,
		locked_at=NULL,
		last_error=CASE WHEN EXISTS(SELECT 1 FROM notification_deliveries delivery
			WHERE delivery.notification_id=notification.id AND delivery.status='sent')
			THEN last_error ELSE 'notification expired before delivery' END,
		updated_at=now()
		WHERE notification.created_at < $1 AND notification.status IN ('pending','processing')
		  AND NOT EXISTS(SELECT 1 FROM notification_deliveries delivery
			WHERE delivery.notification_id=notification.id AND delivery.status IN ('pending','processing'))`, expiredBefore); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries(notification_id,device_id,available_at)
		SELECT notification.id,device.id,$1
		FROM notification_outbox notification
		JOIN push_devices device ON device.user_id=notification.user_id AND device.active
		WHERE notification.status IN ('pending','processing') AND notification.available_at <= $1
		  AND notification.created_at >= $3 AND device.platform=ANY($2)
		ON CONFLICT(notification_id,device_id) DO NOTHING`, now, platforms, expiredBefore); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH due AS (
		SELECT delivery.id
		FROM notification_deliveries delivery
		JOIN notification_outbox notification ON notification.id=delivery.notification_id
		JOIN push_devices device ON device.id=delivery.device_id AND device.active
		LEFT JOIN notification_preferences preferences ON preferences.user_id=notification.user_id
		WHERE delivery.status='pending' AND delivery.available_at <= $1
		  AND device.platform=ANY($2)
		  AND notification.created_at >= $4
		  AND (
			preferences.quiet_hours_start IS NULL OR
			preferences.quiet_hours_start = preferences.quiet_hours_end OR
			CASE
				WHEN preferences.quiet_hours_start < preferences.quiet_hours_end THEN
					($1 AT TIME ZONE preferences.timezone)::time < preferences.quiet_hours_start OR
					($1 AT TIME ZONE preferences.timezone)::time >= preferences.quiet_hours_end
				ELSE
					($1 AT TIME ZONE preferences.timezone)::time >= preferences.quiet_hours_end AND
					($1 AT TIME ZONE preferences.timezone)::time < preferences.quiet_hours_start
			END
		  )
		ORDER BY delivery.available_at,delivery.id
		FOR UPDATE OF delivery SKIP LOCKED
		LIMIT $3
	), claimed AS (
		UPDATE notification_deliveries delivery
		SET status='processing',attempts=attempts+1,locked_at=$1,updated_at=now()
		FROM due WHERE delivery.id=due.id
		RETURNING delivery.id,delivery.notification_id,delivery.device_id,delivery.attempts
	)
	SELECT claimed.id,claimed.notification_id,claimed.device_id,claimed.attempts,
		device.platform,device.device_token,device.app_id,device.environment,
		notification.event_type,notification.title,notification.body,notification.payload
	FROM claimed
	JOIN push_devices device ON device.id=claimed.device_id
	JOIN notification_outbox notification ON notification.id=claimed.notification_id
		ORDER BY claimed.id`, now, platforms, limit, expiredBefore)
	if err != nil {
		return nil, err
	}
	deliveries := make([]NotificationDelivery, 0)
	for rows.Next() {
		var delivery NotificationDelivery
		if err := rows.Scan(
			&delivery.ID, &delivery.NotificationID, &delivery.DeviceID, &delivery.Attempts,
			&delivery.Platform, &delivery.DeviceToken, &delivery.AppID, &delivery.Environment,
			&delivery.EventType, &delivery.Title, &delivery.Body, &delivery.Payload,
		); err != nil {
			rows.Close()
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(deliveries) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE notification_outbox notification
			SET status='processing',locked_at=$1,updated_at=now()
			WHERE notification.id IN (
				SELECT DISTINCT delivery.notification_id
				FROM notification_deliveries delivery
				WHERE delivery.id=ANY($2)
			)`, now, deliveryIDs(deliveries)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return deliveries, nil
}

func (r *Repository) CompleteNotificationDelivery(
	ctx context.Context,
	deliveryID int,
	success bool,
	permanent bool,
	deactivateDevice bool,
	errorMessage string,
	retryAt time.Time,
	now time.Time,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var notificationID, deviceID int
	if err := tx.QueryRow(ctx, `SELECT notification_id,device_id
		FROM notification_deliveries WHERE id=$1 AND status='processing' FOR UPDATE`, deliveryID,
	).Scan(&notificationID, &deviceID); err != nil {
		return mapNotFound(err)
	}
	status := "pending"
	if success {
		status = "sent"
	} else if permanent {
		status = "dead"
	}
	_, err = tx.Exec(ctx, `UPDATE notification_deliveries SET
		status=$1,available_at=$2,last_error=NULLIF($3,''),locked_at=NULL,
		sent_at=CASE WHEN $1='sent' THEN $4 ELSE sent_at END,updated_at=now()
		WHERE id=$5`, status, retryAt, errorMessage, now, deliveryID)
	if err != nil {
		return err
	}
	if deactivateDevice {
		if _, err := tx.Exec(ctx, `UPDATE push_devices SET active=false,updated_at=now() WHERE id=$1`, deviceID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE notification_outbox notification SET
		status=CASE
			WHEN EXISTS(SELECT 1 FROM notification_deliveries d
				WHERE d.notification_id=notification.id AND d.status IN ('pending','processing')) THEN 'processing'
			WHEN EXISTS(SELECT 1 FROM notification_deliveries d
				WHERE d.notification_id=notification.id AND d.status='sent') THEN 'sent'
			ELSE 'dead'
		END,
		locked_at=NULL,
		sent_at=CASE WHEN EXISTS(SELECT 1 FROM notification_deliveries d
			WHERE d.notification_id=notification.id AND d.status='sent') THEN COALESCE(sent_at,$2) ELSE sent_at END,
		last_error=CASE WHEN $3='' THEN last_error ELSE $3 END,
		updated_at=now()
		WHERE notification.id=$1`, notificationID, now, errorMessage)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func deliveryIDs(deliveries []NotificationDelivery) []int {
	ids := make([]int, len(deliveries))
	for index, delivery := range deliveries {
		ids[index] = delivery.ID
	}
	return ids
}
