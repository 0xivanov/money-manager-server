package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"money-manager-server/internal/model"
)

func TestMaintenanceWorkersStopCancelsAndJoinsEveryWorker(t *testing.T) {
	maintainer := &blockingMaintenanceService{
		started:   make(chan string, 3),
		cancelled: make(chan string, 3),
		release:   make(chan struct{}),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workers := startMaintenanceWorkers(context.Background(), maintainer, logger, maintenanceIntervals{
		scheduledTransactions: time.Hour,
		openBankingSync:       time.Hour,
		notificationDelivery:  time.Hour,
	})
	released := false
	defer func() {
		if !released {
			close(maintainer.release)
		}
		workers.Stop()
	}()

	waitForWorkerSignals(t, maintainer.started)
	stopped := make(chan struct{})
	go func() {
		workers.Stop()
		close(stopped)
	}()
	waitForWorkerSignals(t, maintainer.cancelled)

	select {
	case <-stopped:
		t.Fatal("Stop returned before the maintenance workers exited")
	default:
	}

	close(maintainer.release)
	released = true
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after every maintenance worker exited")
	}

	workers.Stop()
}

func waitForWorkerSignals(t *testing.T, signals <-chan string) {
	t.Helper()
	seen := make(map[string]bool, 3)
	for len(seen) < 3 {
		select {
		case name := <-signals:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for worker signals; received %v", seen)
		}
	}
}

type blockingMaintenanceService struct {
	started   chan string
	cancelled chan string
	release   chan struct{}
}

func (s *blockingMaintenanceService) run(ctx context.Context, name string) error {
	s.started <- name
	<-ctx.Done()
	s.cancelled <- name
	<-s.release
	return ctx.Err()
}

func (s *blockingMaintenanceService) RunScheduledTransactionMaintenance(ctx context.Context) (model.ScheduleMaintenanceResult, error) {
	return model.ScheduleMaintenanceResult{}, s.run(ctx, "scheduled transactions")
}

func (s *blockingMaintenanceService) RunOpenBankingSyncMaintenance(ctx context.Context) (model.OpenBankingMaintenanceResult, error) {
	return model.OpenBankingMaintenanceResult{}, s.run(ctx, "open banking sync")
}

func (s *blockingMaintenanceService) RunNotificationDeliveryMaintenance(ctx context.Context) (model.NotificationDeliveryResult, error) {
	return model.NotificationDeliveryResult{}, s.run(ctx, "notification delivery")
}
