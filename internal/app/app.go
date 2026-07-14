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
	workers := startMaintenanceWorkers(signalCtx, svc, logger, maintenanceIntervals{
		scheduledTransactions: time.Minute,
		openBankingSync:       5 * time.Minute,
		notificationDelivery:  30 * time.Second,
	})
	// This defer is registered after svc.Close, so workers always join before the store closes.
	defer workers.Stop()
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
	workers.Stop()
	logger.Info("server stopped")
	return nil
}
