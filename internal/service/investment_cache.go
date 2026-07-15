package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"money-manager-server/internal/config"

	"github.com/redis/go-redis/v9"
)

const (
	investmentResponseCacheTTL      = 5 * time.Minute
	investmentCacheOperationTimeout = 200 * time.Millisecond
	investmentCacheNamespace        = "money-manager:investments:v1:user:"
)

type investmentResponseCache interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	InvalidateUser(context.Context, int) error
	Close() error
}

type redisInvestmentResponseCache struct {
	client *redis.Client
}

func newRedisInvestmentResponseCache(redisURL string) (investmentResponseCache, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	options.DialTimeout = investmentCacheOperationTimeout
	options.ReadTimeout = investmentCacheOperationTimeout
	options.WriteTimeout = investmentCacheOperationTimeout
	options.PoolTimeout = investmentCacheOperationTimeout
	return &redisInvestmentResponseCache{client: redis.NewClient(options)}, nil
}

func (c *redisInvestmentResponseCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	contents, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return contents, true, nil
}

func (c *redisInvestmentResponseCache) Set(ctx context.Context, key string, contents []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, contents, ttl).Err()
}

func (c *redisInvestmentResponseCache) InvalidateUser(ctx context.Context, userID int) error {
	keys := []string{
		investmentPortfolioCacheKey(userID),
		investmentPortfolioHistoryCacheKey(userID, "1m"),
		investmentPortfolioHistoryCacheKey(userID, "3m"),
		investmentPortfolioHistoryCacheKey(userID, "1y"),
	}
	return c.client.Unlink(ctx, keys...).Err()
}

func (c *redisInvestmentResponseCache) Close() error {
	return c.client.Close()
}

func (s *Service) configureInvestmentResponseCache(cfg config.Config) {
	if cfg.RedisURL == "" {
		return
	}
	cache, err := newRedisInvestmentResponseCache(cfg.RedisURL)
	if err != nil {
		slog.Warn("investment response cache is disabled", "error", err)
		return
	}
	s.investmentCache = cache
}

func investmentCacheUserPrefix(userID int) string {
	return investmentCacheNamespace + strconv.Itoa(userID) + ":"
}

func investmentPortfolioCacheKey(userID int) string {
	return investmentCacheUserPrefix(userID) + "portfolio"
}

func investmentPortfolioHistoryCacheKey(userID int, rangeValue string) string {
	return investmentCacheUserPrefix(userID) + "history:" + rangeValue
}

func (s *Service) loadInvestmentResponse(ctx context.Context, key string, destination any) bool {
	if s.investmentCache == nil {
		return false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, investmentCacheOperationTimeout)
	defer cancel()
	contents, found, err := s.investmentCache.Get(cacheCtx, key)
	if err != nil {
		slog.Debug("investment response cache read failed", "error", err)
		return false
	}
	if !found {
		return false
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		slog.Warn("investment response cache entry is invalid", "error", err)
		return false
	}
	return true
}

func (s *Service) storeInvestmentResponse(ctx context.Context, key string, value any) {
	if s.investmentCache == nil {
		return
	}
	contents, err := json.Marshal(value)
	if err != nil {
		slog.Warn("encode investment response cache entry", "error", err)
		return
	}
	cacheCtx, cancel := context.WithTimeout(ctx, investmentCacheOperationTimeout)
	defer cancel()
	if err := s.investmentCache.Set(cacheCtx, key, contents, investmentResponseCacheTTL); err != nil {
		slog.Debug("investment response cache write failed", "error", err)
	}
}

func (s *Service) invalidateInvestmentResponses(ctx context.Context, userID int) {
	if s.investmentCache == nil {
		return
	}
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), investmentCacheOperationTimeout)
	defer cancel()
	if err := s.investmentCache.InvalidateUser(cacheCtx, userID); err != nil {
		slog.Warn("investment response cache invalidation failed", "user_id", userID, "error", err)
	}
}
