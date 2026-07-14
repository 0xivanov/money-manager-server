package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"money-manager-server/internal/push"
	"money-manager-server/internal/repository"
)

func TestNotificationDeliveryMaintenanceSendsRetriesAndDeactivates(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	type completion struct {
		id                           int
		success, permanent, disabled bool
		retryAt                      time.Time
	}
	var completions []completion
	store := &fakeStore{
		claimNotificationDeliveries: func(
			_ context.Context, claimedAt, staleBefore time.Time, platforms []string, limit int,
		) ([]repository.NotificationDelivery, error) {
			if !claimedAt.Equal(now) || !staleBefore.Equal(now.Add(-10*time.Minute)) ||
				len(platforms) != 1 || platforms[0] != "ios" || limit != 25 {
				t.Fatalf("claim arguments = %s, %s, %#v, %d", claimedAt, staleBefore, platforms, limit)
			}
			return []repository.NotificationDelivery{
				{ID: 1, Attempts: 1, Platform: "ios", DeviceToken: "good", AppID: "org.moneymanager.ios"},
				{ID: 2, Attempts: 1, Platform: "ios", DeviceToken: "retry", AppID: "org.moneymanager.ios"},
				{ID: 3, Attempts: 1, Platform: "ios", DeviceToken: "invalid", AppID: "org.moneymanager.ios"},
				{ID: 4, Attempts: 8, Platform: "ios", DeviceToken: "retry", AppID: "org.moneymanager.ios"},
			}, nil
		},
		completeNotificationDelivery: func(
			_ context.Context, id int, success, permanent, disabled bool,
			_ string, retryAt, completedAt time.Time,
		) error {
			if !completedAt.Equal(now) {
				t.Fatalf("completed at %s", completedAt)
			}
			completions = append(completions, completion{id, success, permanent, disabled, retryAt})
			return nil
		},
	}
	service := testService(store)
	service.now = func() time.Time { return now }
	service.pushPlatforms = []string{"ios"}
	service.pushSenders = map[string]notificationSender{"ios": fakeNotificationSender(func(_ context.Context, notification push.Notification) (push.Result, error) {
		switch notification.DeviceToken {
		case "good":
			return push.Result{}, nil
		case "invalid":
			return push.Result{Permanent: true, Deactivate: true}, errors.New("invalid device")
		default:
			return push.Result{}, errors.New("temporary failure")
		}
	})}

	result, err := service.RunNotificationDeliveryMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 4 || result.Sent != 1 || result.Retrying != 1 ||
		result.Dead != 2 || result.Deactivated != 1 {
		t.Fatalf("delivery result = %#v", result)
	}
	if len(completions) != 4 || !completions[0].success ||
		!completions[1].retryAt.Equal(now.Add(30*time.Second)) ||
		!completions[2].permanent || !completions[2].disabled ||
		!completions[3].permanent {
		t.Fatalf("completions = %#v", completions)
	}
}

type fakeNotificationSender func(context.Context, push.Notification) (push.Result, error)

func (sender fakeNotificationSender) Send(ctx context.Context, notification push.Notification) (push.Result, error) {
	return sender(ctx, notification)
}
