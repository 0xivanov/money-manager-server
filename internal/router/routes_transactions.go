package router

import (
	"fmt"
	"io"
	"mime"
	"net/http"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

func (h *handler) registerCategoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /categories", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		categories, err := h.api.ListCategories(request.Context(), userID, request.URL.Query().Get("type"))
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, categories, err)
	}))
	mux.HandleFunc("POST /categories", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.CategoryRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		category, err := h.api.CreateCategory(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, category, err)
	}))
	mux.HandleFunc("DELETE /categories/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, categoryID int) {
		if err := h.api.DeleteCategory(request.Context(), userID, categoryID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}

func (h *handler) registerTransactionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /transactions", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		query := request.URL.Query()
		transactions, err := h.api.ListTransactions(
			request.Context(), userID, query.Get("month"), query.Get("type"), query.Get("category"),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, transactions, err)
	}))
	mux.HandleFunc("GET /transactions/export", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		from, to := request.URL.Query().Get("from"), request.URL.Query().Get("to")
		transactions, err := h.api.ExportTransactions(request.Context(), userID, from, to)
		if err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		contents, err := transactionsCSV(transactions)
		if err != nil {
			writeError(w, request, h.options.Logger, apperrors.Internal(fmt.Errorf("encode CSV: %w", err)))
			return
		}
		writeCSV(w, fmt.Sprintf("money-manager-%s-to-%s.csv", from, to), contents)
	}))
	mux.HandleFunc("POST /transactions/import/revolut", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || (mediaType != "text/csv" && mediaType != "application/csv" && mediaType != "application/vnd.ms-excel") {
			writeError(w, request, h.options.Logger, apperrors.Validation("Content-Type must be text/csv"))
			return
		}
		request.Body = http.MaxBytesReader(w, request.Body, 2*1024*1024)
		contents, err := io.ReadAll(request.Body)
		if err != nil {
			writeError(w, request, h.options.Logger, apperrors.Validation("CSV file is too large"))
			return
		}
		result, err := h.api.ImportRevolutCSV(request.Context(), userID, contents)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, result, err)
	}))
	mux.HandleFunc("GET /transactions/summary", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		summary, err := h.api.Summary(request.Context(), userID, request.URL.Query().Get("month"))
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, summary, err)
	}))
	mux.HandleFunc("POST /transactions", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.TransactionRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		transaction, err := h.api.CreateTransaction(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, transaction, err)
	}))
	mux.HandleFunc("PUT /transactions/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, transactionID int) {
		var payload model.TransactionRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		transaction, err := h.api.UpdateTransaction(request.Context(), userID, transactionID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, transaction, err)
	}))
	mux.HandleFunc("DELETE /transactions/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, transactionID int) {
		if err := h.api.DeleteTransaction(request.Context(), userID, transactionID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
