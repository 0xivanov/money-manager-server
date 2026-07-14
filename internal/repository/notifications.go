package repository

import (
	"context"

	"money-manager-server/internal/model"
)

func (r *Repository) GetNotificationPreferences(ctx context.Context, userID int) (model.NotificationPreferences, error) {
	if _, err := r.db.Exec(ctx, `INSERT INTO notification_preferences(user_id) VALUES($1)
		ON CONFLICT(user_id) DO NOTHING`, userID); err != nil {
		return model.NotificationPreferences{}, err
	}
	return scanNotificationPreferences(r.db.QueryRow(ctx, `SELECT bank_spending,budget_alerts,scheduled_money,
		investment_reminders,COALESCE(to_char(quiet_hours_start,'HH24:MI'),''),
		COALESCE(to_char(quiet_hours_end,'HH24:MI'),''),timezone
		FROM notification_preferences WHERE user_id=$1`, userID))
}

func (r *Repository) UpdateNotificationPreferences(
	ctx context.Context,
	userID int,
	preferences model.NotificationPreferences,
) (model.NotificationPreferences, error) {
	_, err := r.db.Exec(ctx, `INSERT INTO notification_preferences(
		user_id,bank_spending,budget_alerts,scheduled_money,investment_reminders,
		quiet_hours_start,quiet_hours_end,timezone
	) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::time,NULLIF($7,'')::time,$8)
	ON CONFLICT(user_id) DO UPDATE SET
		bank_spending=EXCLUDED.bank_spending,budget_alerts=EXCLUDED.budget_alerts,
		scheduled_money=EXCLUDED.scheduled_money,investment_reminders=EXCLUDED.investment_reminders,
		quiet_hours_start=EXCLUDED.quiet_hours_start,quiet_hours_end=EXCLUDED.quiet_hours_end,
		timezone=EXCLUDED.timezone,updated_at=now()`, userID, preferences.BankSpending,
		preferences.BudgetAlerts, preferences.ScheduledMoney, preferences.InvestmentReminders,
		preferences.QuietHoursStart, preferences.QuietHoursEnd, preferences.Timezone)
	if err != nil {
		return model.NotificationPreferences{}, err
	}
	return r.GetNotificationPreferences(ctx, userID)
}

func (r *Repository) RegisterPushDevice(
	ctx context.Context,
	userID int,
	request model.PushDeviceRequest,
) (model.PushDevice, error) {
	var item model.PushDevice
	err := r.db.QueryRow(ctx, `INSERT INTO push_devices(
		user_id,platform,device_token,app_id,environment
	) VALUES($1,$2,$3,$4,$5)
	ON CONFLICT(platform,device_token) DO UPDATE SET
		user_id=EXCLUDED.user_id,app_id=EXCLUDED.app_id,environment=EXCLUDED.environment,
		active=true,last_seen_at=now(),updated_at=now()
	RETURNING id,platform,app_id,environment,
		to_char(last_seen_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		userID, request.Platform, request.DeviceToken, request.AppID, request.Environment,
	).Scan(&item.ID, &item.Platform, &item.AppID, &item.Environment, &item.LastSeenAt)
	return item, err
}

func (r *Repository) DeactivatePushDevice(ctx context.Context, userID, deviceID int) error {
	tag, err := r.db.Exec(ctx, `UPDATE push_devices SET active=false,updated_at=now()
		WHERE id=$1 AND user_id=$2 AND active`, deviceID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanNotificationPreferences(row rowScanner) (model.NotificationPreferences, error) {
	var item model.NotificationPreferences
	err := row.Scan(&item.BankSpending, &item.BudgetAlerts, &item.ScheduledMoney,
		&item.InvestmentReminders, &item.QuietHoursStart, &item.QuietHoursEnd, &item.Timezone)
	return item, err
}
