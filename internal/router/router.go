package router

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

type API interface {
	Ready(context.Context) error
	Register(context.Context, model.AuthRequest) (model.AuthResponse, error)
	Login(context.Context, model.AuthRequest) (model.AuthResponse, error)
	Authenticate(context.Context, string) (int, error)
	GetMe(context.Context, int) (model.User, error)
	DeleteMe(context.Context, int) error
	ListCategories(context.Context, int, string) ([]model.Category, error)
	CreateCategory(context.Context, int, model.CategoryRequest) (model.Category, error)
	DeleteCategory(context.Context, int, int) error
	ListTransactions(context.Context, int, string, string, string) ([]model.Transaction, error)
	ExportTransactions(context.Context, int, string, string) ([]model.Transaction, error)
	Summary(context.Context, int, string) (model.Summary, error)
	CreateTransaction(context.Context, int, model.TransactionRequest) (model.Transaction, error)
	UpdateTransaction(context.Context, int, int, model.TransactionRequest) (model.Transaction, error)
	DeleteTransaction(context.Context, int, int) error
	ImportRevolutCSV(context.Context, int, []byte) (model.ImportResult, error)
	ListOpenBankingInstitutions(context.Context, string, string) ([]model.OpenBankingInstitution, error)
	StartOpenBankingAuthorization(context.Context, int, model.OpenBankingAuthorizationRequest) (model.OpenBankingAuthorization, error)
	CompleteOpenBankingAuthorization(context.Context, model.OpenBankingCallbackRequest) (model.OpenBankingCallbackResult, error)
	ListOpenBankingConnections(context.Context, int) ([]model.OpenBankingConnection, error)
	GetOpenBankingConnection(context.Context, int, int) (model.OpenBankingConnection, error)
	DeleteOpenBankingConnection(context.Context, int, int, model.OpenBankingPSUContext) error
	ListOpenBankingAccounts(context.Context, int) ([]model.OpenBankingAccount, error)
	GetOpenBankingAccountDetails(context.Context, int, int, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error)
	GetOpenBankingAccountBalances(context.Context, int, int, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error)
	GetOpenBankingAccountTransactions(context.Context, int, int, string, string, string, string, string, model.OpenBankingPSUContext) (model.OpenBankingProviderData, error)
}

type Options struct {
	RequestBodyLimit  int64
	AuthRateLimit     int
	AuthRateWindow    time.Duration
	TrustedProxyCIDRs []netip.Prefix
	TrustedProxyHops  int
	Logger            *slog.Logger
}

type contextKey string

const requestIDKey contextKey = "request_id"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func Build(api API, options Options) http.Handler {
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
	limiter := newAuthRateLimiter(options.AuthRateLimit, options.AuthRateWindow)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, http.StatusOK, "ok")
	})
	readiness := func(w http.ResponseWriter, request *http.Request) {
		if err := api.Ready(request.Context()); err != nil {
			writeText(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		writeText(w, http.StatusOK, "ok")
	}
	mux.HandleFunc("GET /readyz", readiness)
	mux.HandleFunc("GET /health", readiness)

	mux.HandleFunc("POST /auth/register", func(w http.ResponseWriter, request *http.Request) {
		var payload model.AuthRequest
		if err := decodeJSON(w, request, &payload, options.RequestBodyLimit); err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		if !allowAuthRequest(w, request, payload.Email, limiter, options) {
			return
		}
		response, err := api.Register(request.Context(), payload)
		writeJSONResult(w, request, options.Logger, http.StatusCreated, response, err)
	})
	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, request *http.Request) {
		var payload model.AuthRequest
		if err := decodeJSON(w, request, &payload, options.RequestBodyLimit); err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		if !allowAuthRequest(w, request, payload.Email, limiter, options) {
			return
		}
		response, err := api.Login(request.Context(), payload)
		writeJSONResult(w, request, options.Logger, http.StatusOK, response, err)
	})

	mux.HandleFunc("GET /me", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		user, err := api.GetMe(request.Context(), userID)
		writeJSONResult(w, request, options.Logger, http.StatusOK, user, err)
	})
	mux.HandleFunc("DELETE /me", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		if err := api.DeleteMe(request.Context(), userID); err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /categories", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		categories, err := api.ListCategories(request.Context(), userID, request.URL.Query().Get("type"))
		writeJSONResult(w, request, options.Logger, http.StatusOK, categories, err)
	})
	mux.HandleFunc("POST /categories", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		var payload model.CategoryRequest
		if err := decodeJSON(w, request, &payload, options.RequestBodyLimit); err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		category, err := api.CreateCategory(request.Context(), userID, payload)
		writeJSONResult(w, request, options.Logger, http.StatusCreated, category, err)
	})
	mux.HandleFunc("DELETE /categories/{id}", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		categoryID, err := parseID(request.PathValue("id"))
		if err == nil {
			err = api.DeleteCategory(request.Context(), userID, categoryID)
		}
		if err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /transactions", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		query := request.URL.Query()
		transactions, err := api.ListTransactions(
			request.Context(), userID, query.Get("month"), query.Get("type"), query.Get("category"),
		)
		writeJSONResult(w, request, options.Logger, http.StatusOK, transactions, err)
	})
	mux.HandleFunc("GET /transactions/export", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		from := request.URL.Query().Get("from")
		to := request.URL.Query().Get("to")
		transactions, err := api.ExportTransactions(request.Context(), userID, from, to)
		if err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		contents, err := transactionsCSV(transactions)
		if err != nil {
			writeError(w, request, options.Logger, apperrors.Internal(fmt.Errorf("encode CSV: %w", err)))
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="money-manager-%s-to-%s.csv"`, from, to))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(contents)
	})
	mux.HandleFunc("POST /transactions/import/revolut", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || (mediaType != "text/csv" && mediaType != "application/csv" && mediaType != "application/vnd.ms-excel") {
			writeError(w, request, options.Logger, apperrors.Validation("Content-Type must be text/csv"))
			return
		}
		request.Body = http.MaxBytesReader(w, request.Body, 2*1024*1024)
		contents, err := io.ReadAll(request.Body)
		if err != nil {
			writeError(w, request, options.Logger, apperrors.Validation("CSV file is too large"))
			return
		}
		result, err := api.ImportRevolutCSV(request.Context(), userID, contents)
		writeJSONResult(w, request, options.Logger, http.StatusOK, result, err)
	})
	mux.HandleFunc("GET /transactions/summary", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		summary, err := api.Summary(request.Context(), userID, request.URL.Query().Get("month"))
		writeJSONResult(w, request, options.Logger, http.StatusOK, summary, err)
	})
	mux.HandleFunc("POST /transactions", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		var payload model.TransactionRequest
		if err := decodeJSON(w, request, &payload, options.RequestBodyLimit); err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		transaction, err := api.CreateTransaction(request.Context(), userID, payload)
		writeJSONResult(w, request, options.Logger, http.StatusCreated, transaction, err)
	})
	mux.HandleFunc("PUT /transactions/{id}", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		transactionID, err := parseID(request.PathValue("id"))
		if err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		var payload model.TransactionRequest
		if err := decodeJSON(w, request, &payload, options.RequestBodyLimit); err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		transaction, err := api.UpdateTransaction(request.Context(), userID, transactionID, payload)
		writeJSONResult(w, request, options.Logger, http.StatusOK, transaction, err)
	})
	mux.HandleFunc("DELETE /transactions/{id}", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		transactionID, err := parseID(request.PathValue("id"))
		if err == nil {
			err = api.DeleteTransaction(request.Context(), userID, transactionID)
		}
		if err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/open-banking/banks", func(w http.ResponseWriter, request *http.Request) {
		if _, ok := authenticatedUser(w, request, api, options.Logger); !ok {
			return
		}
		institutions, err := api.ListOpenBankingInstitutions(
			request.Context(), request.URL.Query().Get("country"), request.URL.Query().Get("psu_type"),
		)
		writeJSONResult(w, request, options.Logger, http.StatusOK, institutions, err)
	})
	mux.HandleFunc("POST /api/open-banking/authorizations", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		var payload model.OpenBankingAuthorizationRequest
		if err := decodeJSON(w, request, &payload, options.RequestBodyLimit); err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		authorization, err := api.StartOpenBankingAuthorization(request.Context(), userID, payload)
		writeJSONResult(w, request, options.Logger, http.StatusCreated, authorization, err)
	})
	mux.HandleFunc("GET /api/open-banking/callback", func(w http.ResponseWriter, request *http.Request) {
		result, err := api.CompleteOpenBankingAuthorization(request.Context(), model.OpenBankingCallbackRequest{
			State: request.URL.Query().Get("state"), Code: request.URL.Query().Get("code"),
			Error: request.URL.Query().Get("error"), ErrorDescription: request.URL.Query().Get("error_description"),
		})
		if result.RedirectURL != "" {
			if err != nil {
				logRequestFailure(request, options.Logger, err)
			}
			http.Redirect(w, request, result.RedirectURL, http.StatusSeeOther)
			return
		}
		if result.Status != "" {
			status := http.StatusOK
			if err != nil {
				status = http.StatusBadGateway
				if apperrors.KindOf(err) == apperrors.KindValidation {
					status = http.StatusBadRequest
				}
				logRequestFailure(request, options.Logger, err)
			}
			writeOpenBankingCallbackPage(w, status, result)
			return
		}
		writeError(w, request, options.Logger, err)
	})
	mux.HandleFunc("GET /api/open-banking/connections", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		connections, err := api.ListOpenBankingConnections(request.Context(), userID)
		writeJSONResult(w, request, options.Logger, http.StatusOK, connections, err)
	})
	mux.HandleFunc("GET /api/open-banking/connections/{id}", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		connectionID, err := parseID(request.PathValue("id"))
		if err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		connection, err := api.GetOpenBankingConnection(request.Context(), userID, connectionID)
		writeJSONResult(w, request, options.Logger, http.StatusOK, connection, err)
	})
	mux.HandleFunc("DELETE /api/open-banking/connections/{id}", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		connectionID, err := parseID(request.PathValue("id"))
		if err == nil {
			err = api.DeleteOpenBankingConnection(request.Context(), userID, connectionID, openBankingPSUContext(request, options))
		}
		if err != nil {
			writeError(w, request, options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/open-banking/accounts", func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, api, options.Logger)
		if !ok {
			return
		}
		accounts, err := api.ListOpenBankingAccounts(request.Context(), userID)
		writeJSONResult(w, request, options.Logger, http.StatusOK, accounts, err)
	})
	mux.HandleFunc("GET /api/open-banking/accounts/{id}/details", func(w http.ResponseWriter, request *http.Request) {
		userID, accountID, ok := authenticatedOpenBankingAccount(w, request, api, options.Logger)
		if !ok {
			return
		}
		response, err := api.GetOpenBankingAccountDetails(request.Context(), userID, accountID, openBankingPSUContext(request, options))
		writeJSONResult(w, request, options.Logger, http.StatusOK, response, err)
	})
	mux.HandleFunc("GET /api/open-banking/accounts/{id}/balances", func(w http.ResponseWriter, request *http.Request) {
		userID, accountID, ok := authenticatedOpenBankingAccount(w, request, api, options.Logger)
		if !ok {
			return
		}
		response, err := api.GetOpenBankingAccountBalances(request.Context(), userID, accountID, openBankingPSUContext(request, options))
		writeJSONResult(w, request, options.Logger, http.StatusOK, response, err)
	})
	mux.HandleFunc("GET /api/open-banking/accounts/{id}/transactions", func(w http.ResponseWriter, request *http.Request) {
		userID, accountID, ok := authenticatedOpenBankingAccount(w, request, api, options.Logger)
		if !ok {
			return
		}
		query := request.URL.Query()
		response, err := api.GetOpenBankingAccountTransactions(
			request.Context(), userID, accountID, query.Get("date_from"), query.Get("date_to"),
			query.Get("continuation_key"), query.Get("transaction_status"), query.Get("strategy"),
			openBankingPSUContext(request, options),
		)
		writeJSONResult(w, request, options.Logger, http.StatusOK, response, err)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, request *http.Request) {
		writeError(w, request, options.Logger, apperrors.NotFound("endpoint not found"))
	})

	return observeRequests(mux, options.Logger, options.TrustedProxyCIDRs, options.TrustedProxyHops)
}

func decodeJSON(w http.ResponseWriter, request *http.Request, destination any, limit int64) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return apperrors.Validation("Content-Type must be application/json")
	}
	request.Body = http.MaxBytesReader(w, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maximumBytesError *http.MaxBytesError
		if errors.As(err, &maximumBytesError) {
			return apperrors.Validation("request body is too large")
		}
		return apperrors.Validation("request body must contain one valid JSON object with known fields")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return apperrors.Validation("request body must contain exactly one JSON object")
	}
	return nil
}

func authenticatedUser(w http.ResponseWriter, request *http.Request, api API, logger *slog.Logger) (int, bool) {
	fields := strings.Fields(request.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		writeError(w, request, logger, apperrors.Unauthorized("authorization bearer token is required"))
		return 0, false
	}
	userID, err := api.Authenticate(request.Context(), fields[1])
	if err != nil {
		writeError(w, request, logger, err)
		return 0, false
	}
	return userID, true
}

func authenticatedOpenBankingAccount(w http.ResponseWriter, request *http.Request, api API, logger *slog.Logger) (int, int, bool) {
	userID, ok := authenticatedUser(w, request, api, logger)
	if !ok {
		return 0, 0, false
	}
	accountID, err := parseID(request.PathValue("id"))
	if err != nil {
		writeError(w, request, logger, err)
		return 0, 0, false
	}
	return userID, accountID, true
}

func openBankingPSUContext(request *http.Request, options Options) model.OpenBankingPSUContext {
	return model.OpenBankingPSUContext{
		IPAddress:      clientIP(request, options.TrustedProxyCIDRs, options.TrustedProxyHops),
		UserAgent:      truncateHeader(request.UserAgent(), 512),
		Referer:        truncateHeader(request.Referer(), 1024),
		Accept:         truncateHeader(request.Header.Get("Accept"), 512),
		AcceptCharset:  truncateHeader(request.Header.Get("Accept-Charset"), 256),
		AcceptEncoding: truncateHeader(request.Header.Get("Accept-Encoding"), 256),
		AcceptLanguage: truncateHeader(request.Header.Get("Accept-Language"), 256),
	}
}

func truncateHeader(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}

func parseID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, apperrors.Validation("id must be a positive integer")
	}
	return id, nil
}

func transactionsCSV(transactions []model.Transaction) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{"occurred_at", "type", "category", "description", "amount", "currency"}); err != nil {
		return nil, err
	}
	for _, transaction := range transactions {
		if err := writer.Write([]string{
			transaction.OccurredAt,
			transaction.Type,
			transaction.Category,
			transaction.Description,
			transaction.Amount,
			transaction.Currency,
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeJSONResult(w http.ResponseWriter, request *http.Request, logger *slog.Logger, status int, value any, err error) {
	if err != nil {
		writeError(w, request, logger, err)
		return
	}
	writeJSON(w, status, value)
}

func writeError(w http.ResponseWriter, request *http.Request, logger *slog.Logger, err error) {
	status := http.StatusInternalServerError
	switch apperrors.KindOf(err) {
	case apperrors.KindValidation:
		status = http.StatusBadRequest
	case apperrors.KindUnauthorized:
		status = http.StatusUnauthorized
		w.Header().Set("WWW-Authenticate", `Bearer realm="money-manager"`)
	case apperrors.KindNotFound:
		status = http.StatusNotFound
	case apperrors.KindConflict:
		status = http.StatusConflict
	case apperrors.KindRateLimited:
		status = http.StatusTooManyRequests
	case apperrors.KindUnavailable:
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "30")
	}
	if status == http.StatusInternalServerError || status == http.StatusServiceUnavailable {
		logRequestFailure(request, logger, err)
	}
	writeJSON(w, status, map[string]string{
		"error":      apperrors.PublicMessage(err),
		"request_id": requestIDFromContext(request.Context()),
	})
}

func logRequestFailure(request *http.Request, logger *slog.Logger, err error) {
	cause := errors.Unwrap(err)
	if cause == nil {
		cause = err
	}
	logger.ErrorContext(request.Context(), "request failed",
		"request_id", requestIDFromContext(request.Context()),
		"error", cause,
	)
}

func writeOpenBankingCallbackPage(w http.ResponseWriter, status int, result model.OpenBankingCallbackResult) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	title := "Money Manager"
	message := html.EscapeString(result.Message)
	_, _ = io.WriteString(w, fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>body{margin:0;background:#0d1117;color:#f2f5f7;font:16px system-ui;display:grid;min-height:100vh;place-items:center}.card{max-width:28rem;margin:1.5rem;padding:2rem;border:1px solid #28313b;border-radius:20px;background:#151b23;text-align:center}h1{font-size:1.35rem;margin:0 0 .75rem}p{color:#aeb8c4;line-height:1.5;margin:0}</style></head><body><main class="card"><h1>%s</h1><p>%s</p></main></body></html>`, title, title, message))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeText(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, value)
}

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

func clientIP(request *http.Request, trustedProxyCIDRs []netip.Prefix, trustedProxyHops int) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || host == "" {
		host = request.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return request.RemoteAddr
	}
	remote = remote.Unmap()
	if trustedProxyHops <= 0 || !addressInPrefixes(remote, trustedProxyCIDRs) {
		return remote.String()
	}

	forwardedValues := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	forwarded := make([]netip.Addr, 0, len(forwardedValues))
	for _, value := range forwardedValues {
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			forwarded = nil
			break
		}
		forwarded = append(forwarded, address.Unmap())
	}
	clientIndex := len(forwarded) - trustedProxyHops
	if clientIndex >= 0 {
		for index := clientIndex + 1; index < len(forwarded); index++ {
			if !addressInPrefixes(forwarded[index], trustedProxyCIDRs) {
				return remote.String()
			}
		}
		return forwarded[clientIndex].String()
	}
	if trustedProxyHops == 1 {
		if realIP, err := netip.ParseAddr(strings.TrimSpace(request.Header.Get("X-Real-IP"))); err == nil {
			return realIP.Unmap().String()
		}
	}
	return remote.String()
}

func addressInPrefixes(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

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
