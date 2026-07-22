package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"money-manager-server/internal/model"
)

type scheduledTransactionMaintainer interface {
	RunScheduledTransactionMaintenance(context.Context) (model.ScheduleMaintenanceResult, error)
}

type openBankingSyncMaintainer interface {
	RunOpenBankingSyncMaintenance(context.Context) (model.OpenBankingMaintenanceResult, error)
}

type notificationDeliveryMaintainer interface {
	RunNotificationDeliveryMaintenance(context.Context) (model.NotificationDeliveryResult, error)
}

type maintenanceService interface {
	scheduledTransactionMaintainer
	openBankingSyncMaintainer
	notificationDeliveryMaintainer
}

type maintenanceIntervals struct {
	scheduledTransactions time.Duration
	openBankingSync       time.Duration
	notificationDelivery  time.Duration
}

type maintenanceWorkers struct {
	cancel   context.CancelFunc
	wait     sync.WaitGroup
	stopOnce sync.Once
}

func startMaintenanceWorkers(
	parent context.Context,
	service maintenanceService,
	logger *slog.Logger,
	intervals maintenanceIntervals,
) *maintenanceWorkers {
	ctx, cancel := context.WithCancel(parent)
	workers := &maintenanceWorkers{cancel: cancel}
	workers.start(func() {
		runScheduledTransactionWorker(ctx, service, logger, intervals.scheduledTransactions)
	})
	workers.start(func() {
		runOpenBankingSyncWorker(ctx, service, logger, intervals.openBankingSync)
	})
	workers.start(func() {
		runNotificationDeliveryWorker(ctx, service, logger, intervals.notificationDelivery)
	})
	return workers
}

func (w *maintenanceWorkers) start(run func()) {
	w.wait.Add(1)
	go func() {
		defer w.wait.Done()
		run()
	}()
}

func (w *maintenanceWorkers) Stop() {
	w.stopOnce.Do(func() {
		w.cancel()
		w.wait.Wait()
	})
}

func runNotificationDeliveryWorker(
	ctx context.Context,
	maintainer notificationDeliveryMaintainer,
	logger *slog.Logger,
	interval time.Duration,
) {
	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, min(interval, 25*time.Second))
		defer cancel()
		result, err := maintainer.RunNotificationDeliveryMaintenance(runCtx)
		if err != nil {
			if ctx.Err() == nil {
				logger.ErrorContext(ctx, "notification delivery failed", "error", err,
					"claimed", result.Claimed, "sent", result.Sent,
					"retrying", result.Retrying, "dead", result.Dead,
				)
			}
			return
		}
		if result.Claimed > 0 {
			logger.InfoContext(ctx, "notification delivery completed",
				"claimed", result.Claimed, "sent", result.Sent,
				"retrying", result.Retrying, "dead", result.Dead,
				"deactivated", result.Deactivated,
			)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func runOpenBankingSyncWorker(
	ctx context.Context,
	maintainer openBankingSyncMaintainer,
	logger *slog.Logger,
	interval time.Duration,
) {
	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, min(interval, 4*time.Minute))
		defer cancel()
		result, err := maintainer.RunOpenBankingSyncMaintenance(runCtx)
		if err != nil {
			if ctx.Err() == nil {
				logger.ErrorContext(ctx, "open banking background sync failed",
					"error", err,
					"claimed", result.Claimed,
					"succeeded", result.Succeeded,
					"failed", result.Failed,
				)
			}
			return
		}
		if result.Claimed > 0 {
			logger.InfoContext(ctx, "open banking background sync completed",
				"claimed", result.Claimed,
				"succeeded", result.Succeeded,
				"imported", result.Imported,
				"updated", result.Updated,
				"notifications", result.Notifications,
			)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func runScheduledTransactionWorker(
	ctx context.Context,
	maintainer scheduledTransactionMaintainer,
	logger *slog.Logger,
	interval time.Duration,
) {
	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, min(interval, 45*time.Second))
		defer cancel()
		result, err := maintainer.RunScheduledTransactionMaintenance(runCtx)
		if err != nil {
			if ctx.Err() == nil {
				cause := errors.Unwrap(err)
				if cause == nil {
					cause = err
				}
				logger.ErrorContext(ctx, "scheduled transaction maintenance failed", "error", cause)
			}
			return
		}
		if result.Materialized > 0 || result.Posted > 0 || result.ScheduleReminders > 0 || result.BudgetAlerts > 0 || result.InvestmentPosted > 0 {
			logger.InfoContext(ctx, "scheduled transaction maintenance completed",
				"materialized", result.Materialized,
				"posted", result.Posted,
				"schedule_reminders", result.ScheduleReminders,
				"budget_alerts", result.BudgetAlerts,
				"investment_posted", result.InvestmentPosted,
			)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
