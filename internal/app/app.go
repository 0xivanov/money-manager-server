package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"money-manager-server/internal/config"
	"money-manager-server/internal/model"
	"money-manager-server/internal/router"
	"money-manager-server/internal/service"
)

func Run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	svc, err := service.New(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("initialize service: %w", err)
	}
	defer svc.Close()

	handler := router.Build(svc, router.Options{
		RequestBodyLimit:  cfg.RequestBodyLimit,
		AuthRateLimit:     cfg.AuthRateLimit,
		AuthRateWindow:    cfg.AuthRateWindow,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
		TrustedProxyHops:  cfg.TrustedProxyHops,
		Logger:            logger,
	})
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go runScheduledTransactionWorker(signalCtx, svc, logger, time.Minute)
	go runOpenBankingSyncWorker(signalCtx, svc, logger, 5*time.Minute)
	go runNotificationDeliveryWorker(signalCtx, svc, logger, 30*time.Second)
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-signalCtx.Done():
		logger.Info("shutdown signal received")
		stopSignals()
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("stop HTTP server: %w", err)
		}
	case <-time.After(time.Second):
		return errors.New("HTTP server did not stop after shutdown")
	}
	logger.Info("server stopped")
	return nil
}

type scheduledTransactionMaintainer interface {
	RunScheduledTransactionMaintenance(context.Context) (model.ScheduleMaintenanceResult, error)
}

type openBankingSyncMaintainer interface {
	RunOpenBankingSyncMaintenance(context.Context) (model.OpenBankingMaintenanceResult, error)
}

type notificationDeliveryMaintainer interface {
	RunNotificationDeliveryMaintenance(context.Context) (model.NotificationDeliveryResult, error)
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
				logger.ErrorContext(ctx, "scheduled transaction maintenance failed", "error", err)
			}
			return
		}
		if result.Materialized > 0 || result.Posted > 0 || result.ScheduleReminders > 0 || result.BudgetAlerts > 0 || result.InvestmentReminders > 0 {
			logger.InfoContext(ctx, "scheduled transaction maintenance completed",
				"materialized", result.Materialized,
				"posted", result.Posted,
				"schedule_reminders", result.ScheduleReminders,
				"budget_alerts", result.BudgetAlerts,
				"investment_reminders", result.InvestmentReminders,
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
