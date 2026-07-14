package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"time"

	"money-manager-server/internal/apperrors"
)

type contextKey string

const requestIDKey contextKey = "request_id"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(contents []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(contents)
	r.bytes += written
	return written, err
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func observeRequests(next http.Handler, logger *slog.Logger, trustedProxyCIDRs []netip.Prefix, trustedProxyHops int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
			requestID = newRequestID()
		}
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		request = request.WithContext(ctx)
		w.Header().Set("X-Request-ID", requestID)
		recorder := &responseRecorder{ResponseWriter: w}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(ctx, "request panic", "request_id", requestID, "panic", recovered)
				if recorder.status == 0 {
					writeError(recorder, request, logger, apperrors.Internal(errors.New("request handler panic")))
				}
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.InfoContext(ctx, "http request",
				"request_id", requestID,
				"method", request.Method,
				"path", request.URL.Path,
				"status", status,
				"bytes", recorder.bytes,
				"duration_ms", time.Since(started).Milliseconds(),
				"client_ip", clientIP(request, trustedProxyCIDRs, trustedProxyHops),
			)
		}()
		next.ServeHTTP(recorder, request)
	})
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err == nil {
		return hex.EncodeToString(buffer)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey).(string)
	return requestID
}
