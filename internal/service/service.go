package service

import (
	"context"
	"time"

	"money-manager-server/internal/config"
	"money-manager-server/internal/push"
	"money-manager-server/internal/repository"
)

type Service struct {
	store               Store
	secret              []byte
	issuer              string
	audience            string
	tokenTTL            time.Duration
	legacyAcceptUntil   time.Time
	now                 func() time.Time
	openBanking         openBankingClient
	openBankingConfig   openBankingServiceConfig
	openBankingError    error
	marketData          investmentMarketDataClient
	stockMarketData     stockInvestmentMarketDataClient
	stockHistoryData    stockInvestmentHistoryClient
	trading212OwnerID   int
	investmentCache     investmentResponseCache
	pushSenders         map[string]notificationSender
	pushPlatforms       []string
	pushError           error
	scheduleHorizonDays int
}

type notificationSender interface {
	Send(context.Context, push.Notification) (push.Result, error)
}

func New(ctx context.Context, cfg config.Config) (*Service, error) {
	connectCtx, cancelConnect := context.WithTimeout(ctx, cfg.StartupTimeout)
	db, err := repository.Open(connectCtx, cfg.DatabaseURL, repository.Options{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   cfg.DBMaxConnLifetime,
		MaxConnIdleTime:   cfg.DBMaxConnIdleTime,
		HealthCheckPeriod: cfg.DBHealthCheckPeriod,
	})
	cancelConnect()
	if err != nil {
		return nil, err
	}

	migrationCtx, cancelMigration := context.WithTimeout(ctx, cfg.MigrationTimeout)
	defer cancelMigration()
	if err := repository.Migrate(migrationCtx, db); err != nil {
		db.Close()
		return nil, err
	}
	return NewWithStore(repository.New(db), cfg), nil
}

func NewWithStore(store Store, cfg config.Config) *Service {
	result := &Service{
		store:               store,
		secret:              []byte(cfg.JWTSecret),
		issuer:              cfg.JWTIssuer,
		audience:            cfg.JWTAudience,
		tokenTTL:            cfg.JWTTTL,
		legacyAcceptUntil:   cfg.JWTLegacyAcceptUntil,
		now:                 time.Now,
		scheduleHorizonDays: 90,
		trading212OwnerID:   cfg.Trading212OwnerUserID,
	}
	result.configureOpenBanking(cfg)
	result.configureInvestmentMarketData(cfg)
	result.configureInvestmentResponseCache(cfg)
	result.configurePush(cfg)
	return result
}

func (s *Service) canUseTrading212(userID int) bool {
	return s.stockMarketData != nil && s.trading212OwnerID > 0 && userID == s.trading212OwnerID
}

func (s *Service) Close() {
	if s.investmentCache != nil {
		_ = s.investmentCache.Close()
	}
	s.store.Close()
}

func (s *Service) Ready(ctx context.Context) error {
	return s.store.Ping(ctx)
}
