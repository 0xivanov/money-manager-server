package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/push"
	"money-manager-server/internal/repository"
)

const (
	maximumNotificationDeliveryAttempts = 8
	notificationDeliveryBatchSize       = 25
	notificationDeliveryLockTTL         = 10 * time.Minute
)

func (s *Service) RunNotificationDeliveryMaintenance(ctx context.Context) (model.NotificationDeliveryResult, error) {
	if len(s.pushSenders) == 0 {
		if s.pushError != nil {
			return model.NotificationDeliveryResult{}, apperrors.Internal(fmt.Errorf("configure push delivery: %w", s.pushError))
		}
		return model.NotificationDeliveryResult{}, nil
	}
	now := s.now().UTC().Truncate(time.Second)
	deliveries, err := s.store.ClaimNotificationDeliveries(
		ctx, now, now.Add(-notificationDeliveryLockTTL), s.pushPlatforms, notificationDeliveryBatchSize,
	)
	if err != nil {
		return model.NotificationDeliveryResult{}, apperrors.Internal(fmt.Errorf("claim notification deliveries: %w", err))
	}
	result := model.NotificationDeliveryResult{Claimed: len(deliveries)}
	var completionErrors []error
	for _, delivery := range deliveries {
		sender := s.pushSenders[delivery.Platform]
		if sender == nil {
			completionErrors = append(completionErrors, fmt.Errorf("no push sender for platform %q", delivery.Platform))
			continue
		}
		sendResult, sendErr := sender.Send(ctx, push.Notification{
			DeviceToken: delivery.DeviceToken, AppID: delivery.AppID, Environment: delivery.Environment,
			Title: delivery.Title, Body: delivery.Body, EventType: delivery.EventType, Data: delivery.Payload,
		})
		success := sendErr == nil
		permanent := sendResult.Permanent || delivery.Attempts >= maximumNotificationDeliveryAttempts
		errorMessage := ""
		if sendErr != nil {
			errorMessage = truncateRunes(sendErr.Error(), 1000)
		}
		retryAt := now
		if !success && !permanent {
			retryAt = now.Add(notificationRetryDelay(delivery.Attempts))
		}
		if err := s.store.CompleteNotificationDelivery(
			ctx, delivery.ID, success, permanent, sendResult.Deactivate,
			errorMessage, retryAt, now,
		); err != nil {
			completionErrors = append(completionErrors, fmt.Errorf("complete notification delivery %d: %w", delivery.ID, err))
			continue
		}
		switch {
		case success:
			result.Sent++
		case permanent:
			result.Dead++
		default:
			result.Retrying++
		}
		if sendResult.Deactivate {
			result.Deactivated++
		}
	}
	if len(completionErrors) > 0 {
		return result, apperrors.Internal(errors.Join(completionErrors...))
	}
	return result, nil
}

func notificationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	delay := 30 * time.Second * time.Duration(1<<(attempt-1))
	return min(delay, time.Hour)
}

func (s *Service) GetNotificationPreferences(ctx context.Context, userID int) (model.NotificationPreferences, error) {
	item, err := s.store.GetNotificationPreferences(ctx, userID)
	if err != nil {
		return model.NotificationPreferences{}, apperrors.Internal(fmt.Errorf("get notification preferences: %w", err))
	}
	return item, nil
}

func (s *Service) UpdateNotificationPreferences(
	ctx context.Context,
	userID int,
	preferences model.NotificationPreferences,
) (model.NotificationPreferences, error) {
	timezone := strings.TrimSpace(preferences.Timezone)
	if timezone == "" {
		timezone = defaultScheduleTimezone
	}
	if len(timezone) > 100 {
		return model.NotificationPreferences{}, apperrors.Validation("timezone must be 100 characters or less")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return model.NotificationPreferences{}, apperrors.Validation("timezone must be a valid IANA timezone")
	}
	quietStart := strings.TrimSpace(preferences.QuietHoursStart)
	quietEnd := strings.TrimSpace(preferences.QuietHoursEnd)
	if (quietStart == "") != (quietEnd == "") {
		return model.NotificationPreferences{}, apperrors.Validation("quiet_hours_start and quiet_hours_end must be set together")
	}
	for name, value := range map[string]string{"quiet_hours_start": quietStart, "quiet_hours_end": quietEnd} {
		if value != "" {
			parsed, err := time.Parse("15:04", value)
			if err != nil {
				return model.NotificationPreferences{}, apperrors.Validation(name + " must use HH:MM format")
			}
			if value = parsed.Format("15:04"); name == "quiet_hours_start" {
				quietStart = value
			} else {
				quietEnd = value
			}
		}
	}
	if quietStart != "" && quietStart == quietEnd {
		return model.NotificationPreferences{}, apperrors.Validation("quiet hours start and end must be different")
	}
	preferences.Timezone = timezone
	preferences.QuietHoursStart = quietStart
	preferences.QuietHoursEnd = quietEnd
	item, err := s.store.UpdateNotificationPreferences(ctx, userID, preferences)
	if err != nil {
		return model.NotificationPreferences{}, apperrors.Internal(fmt.Errorf("update notification preferences: %w", err))
	}
	return item, nil
}

func (s *Service) RegisterPushDevice(ctx context.Context, userID int, request model.PushDeviceRequest) (model.PushDevice, error) {
	request.Platform = strings.ToLower(strings.TrimSpace(request.Platform))
	if request.Platform != "ios" && request.Platform != "android" {
		return model.PushDevice{}, apperrors.Validation("platform must be ios or android")
	}
	request.DeviceToken = strings.TrimSpace(request.DeviceToken)
	if length := len([]byte(request.DeviceToken)); length < 16 || length > 4096 || !utf8.ValidString(request.DeviceToken) {
		return model.PushDevice{}, apperrors.Validation("device_token must be between 16 and 4096 valid UTF-8 bytes")
	}
	request.AppID = strings.TrimSpace(request.AppID)
	if length := len([]byte(request.AppID)); length < 1 || length > 255 || !utf8.ValidString(request.AppID) {
		return model.PushDevice{}, apperrors.Validation("app_id must be between 1 and 255 valid UTF-8 bytes")
	}
	request.Environment = strings.ToLower(strings.TrimSpace(request.Environment))
	if request.Environment != "sandbox" && request.Environment != "production" {
		return model.PushDevice{}, apperrors.Validation("environment must be sandbox or production")
	}
	item, err := s.store.RegisterPushDevice(ctx, userID, request)
	if err != nil {
		return model.PushDevice{}, apperrors.Internal(fmt.Errorf("register push device: %w", err))
	}
	return item, nil
}

func (s *Service) DeletePushDevice(ctx context.Context, userID, deviceID int) error {
	if err := validateID(deviceID); err != nil {
		return err
	}
	err := s.store.DeactivatePushDevice(ctx, userID, deviceID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("push device not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("deactivate push device: %w", err))
	}
	return nil
}
