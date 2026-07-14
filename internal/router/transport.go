package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

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

func parseID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, apperrors.Validation("id must be a positive integer")
	}
	return id, nil
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

func writeCSV(w http.ResponseWriter, filename string, contents []byte) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(contents)
}
