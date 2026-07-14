package router

import (
	"log/slog"
	"net/http"
	"strings"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

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
