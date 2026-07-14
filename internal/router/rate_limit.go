package router

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"money-manager-server/internal/apperrors"
)

func allowAuthRequest(w http.ResponseWriter, request *http.Request, identifier string, limiter *authRateLimiter, options Options) bool {
	key := authRateLimitKey(request, identifier, options)
	allowed, retryAfter := limiter.Allow(key, time.Now())
	if allowed {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second)/time.Second))))
	writeError(w, request, options.Logger, apperrors.RateLimited("too many authentication attempts; try again later"))
	return false
}

func authRateLimitKey(request *http.Request, identifier string, options Options) string {
	normalizedIdentifier := strings.ToLower(strings.TrimSpace(identifier))
	digest := sha256.Sum256([]byte(normalizedIdentifier))
	return request.URL.Path + "|" + hex.EncodeToString(digest[:16]) + "|" +
		clientIP(request, options.TrustedProxyCIDRs, options.TrustedProxyHops)
}

type rateLimitEntry struct {
	count     int
	resetTime time.Time
}

type authRateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateLimitEntry
	limit   int
	window  time.Duration
	calls   uint64
}

func newAuthRateLimiter(limit int, window time.Duration) *authRateLimiter {
	return &authRateLimiter{entries: make(map[string]rateLimitEntry), limit: limit, window: window}
}

func (l *authRateLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls%256 == 0 {
		for entryKey, entry := range l.entries {
			if !now.Before(entry.resetTime) {
				delete(l.entries, entryKey)
			}
		}
	}
	entry, exists := l.entries[key]
	if !exists || !now.Before(entry.resetTime) {
		l.entries[key] = rateLimitEntry{count: 1, resetTime: now.Add(l.window)}
		return true, 0
	}
	if entry.count >= l.limit {
		return false, entry.resetTime.Sub(now)
	}
	entry.count++
	l.entries[key] = entry
	return true, 0
}
