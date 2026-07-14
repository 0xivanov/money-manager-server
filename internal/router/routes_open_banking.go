package router

import (
	"net/http"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

func (h *handler) registerOpenBankingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/open-banking/banks", h.requireUser(func(w http.ResponseWriter, request *http.Request, _ int) {
		institutions, err := h.api.ListOpenBankingInstitutions(
			request.Context(), request.URL.Query().Get("country"), request.URL.Query().Get("psu_type"),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, institutions, err)
	}))
	mux.HandleFunc("POST /api/open-banking/authorizations", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.OpenBankingAuthorizationRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		authorization, err := h.api.StartOpenBankingAuthorization(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, authorization, err)
	}))
	mux.HandleFunc("GET /api/open-banking/callback", h.openBankingCallback)
	mux.HandleFunc("GET /api/open-banking/connections", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		connections, err := h.api.ListOpenBankingConnections(request.Context(), userID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, connections, err)
	}))
	mux.HandleFunc("GET /api/open-banking/connections/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, connectionID int) {
		connection, err := h.api.GetOpenBankingConnection(request.Context(), userID, connectionID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, connection, err)
	}))
	mux.HandleFunc("DELETE /api/open-banking/connections/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, connectionID int) {
		if err := h.api.DeleteOpenBankingConnection(
			request.Context(), userID, connectionID, openBankingPSUContext(request, h.options),
		); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /api/open-banking/accounts", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		accounts, err := h.api.ListOpenBankingAccounts(request.Context(), userID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, accounts, err)
	}))
	mux.HandleFunc("GET /api/open-banking/accounts/{id}/details", h.requireOpenBankingAccount(func(w http.ResponseWriter, request *http.Request, userID, accountID int) {
		response, err := h.api.GetOpenBankingAccountDetails(
			request.Context(), userID, accountID, openBankingPSUContext(request, h.options),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, response, err)
	}))
	mux.HandleFunc("GET /api/open-banking/accounts/{id}/balances", h.requireOpenBankingAccount(func(w http.ResponseWriter, request *http.Request, userID, accountID int) {
		response, err := h.api.GetOpenBankingAccountBalances(
			request.Context(), userID, accountID, openBankingPSUContext(request, h.options),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, response, err)
	}))
	mux.HandleFunc("GET /api/open-banking/accounts/{id}/transactions", h.requireOpenBankingAccount(func(w http.ResponseWriter, request *http.Request, userID, accountID int) {
		query := request.URL.Query()
		response, err := h.api.GetOpenBankingAccountTransactions(
			request.Context(), userID, accountID, query.Get("date_from"), query.Get("date_to"),
			query.Get("continuation_key"), query.Get("transaction_status"), query.Get("strategy"),
			openBankingPSUContext(request, h.options),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, response, err)
	}))
	mux.HandleFunc("POST /api/open-banking/accounts/{id}/sync", h.requireOpenBankingAccount(func(w http.ResponseWriter, request *http.Request, userID, accountID int) {
		query := request.URL.Query()
		result, err := h.api.SyncOpenBankingAccount(
			request.Context(), userID, accountID, query.Get("date_from"), query.Get("date_to"),
			openBankingPSUContext(request, h.options),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, result, err)
	}))
}

func (h *handler) requireOpenBankingAccount(next authenticatedResourceHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		userID, accountID, ok := authenticatedOpenBankingAccount(w, request, h.api, h.options.Logger)
		if !ok {
			return
		}
		next(w, request, userID, accountID)
	}
}

func (h *handler) openBankingCallback(w http.ResponseWriter, request *http.Request) {
	result, err := h.api.CompleteOpenBankingAuthorization(request.Context(), model.OpenBankingCallbackRequest{
		State: request.URL.Query().Get("state"), Code: request.URL.Query().Get("code"),
		Error: request.URL.Query().Get("error"), ErrorDescription: request.URL.Query().Get("error_description"),
	})
	if result.RedirectURL != "" {
		if err != nil {
			logRequestFailure(request, h.options.Logger, err)
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
			logRequestFailure(request, h.options.Logger, err)
		}
		writeOpenBankingCallbackPage(w, status, result)
		return
	}
	writeError(w, request, h.options.Logger, err)
}
