package router

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"
)

type Options struct {
	RequestBodyLimit  int64
	AuthRateLimit     int
	AuthRateWindow    time.Duration
	TrustedProxyCIDRs []netip.Prefix
	TrustedProxyHops  int
	Logger            *slog.Logger
}

func Build(api API, options Options) http.Handler {
	options = normalizedOptions(options)
	h := &handler{
		api:         api,
		options:     options,
		authLimiter: newAuthRateLimiter(options.AuthRateLimit, options.AuthRateWindow),
	}
	mux := http.NewServeMux()
	registrars := []func(*http.ServeMux){
		h.registerHealthRoutes,
		h.registerAuthRoutes,
		h.registerProfileRoutes,
		h.registerCategoryRoutes,
		h.registerTransactionRoutes,
		h.registerTransactionScheduleRoutes,
		h.registerBudgetRoutes,
		h.registerNotificationRoutes,
		h.registerInvestmentRoutes,
		h.registerInvestmentScheduleRoutes,
		h.registerOpenBankingRoutes,
	}
	for _, register := range registrars {
		register(mux)
	}
	mux.HandleFunc("/", h.notFound)
	return observeRequests(mux, options.Logger, options.TrustedProxyCIDRs, options.TrustedProxyHops)
}

func normalizedOptions(options Options) Options {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.RequestBodyLimit <= 0 {
		options.RequestBodyLimit = 64 * 1024
	}
	if options.AuthRateLimit <= 0 {
		options.AuthRateLimit = 10
	}
	if options.AuthRateWindow <= 0 {
		options.AuthRateWindow = time.Minute
	}
	return options
}
