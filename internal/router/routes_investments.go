package router

import (
	"fmt"
	"net/http"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
)

func (h *handler) registerInvestmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /investments/portfolio", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		item, err := h.api.InvestmentPortfolio(request.Context(), userID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("GET /investments/trades", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		query := request.URL.Query()
		items, err := h.api.ListInvestmentTrades(
			request.Context(), userID, query.Get("from"), query.Get("through"),
			query.Get("asset_type"), query.Get("symbol"), query.Get("broker"),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, items, err)
	}))
	mux.HandleFunc("POST /investments/trades", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.InvestmentTradeRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.CreateInvestmentTrade(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, item, err)
	}))
	mux.HandleFunc("DELETE /investments/trades/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, tradeID int) {
		if err := h.api.DeleteInvestmentTrade(request.Context(), userID, tradeID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("PUT /investments/prices", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.InvestmentPriceRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.SetManualInvestmentPrice(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("GET /investments/export", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		from, through := request.URL.Query().Get("from"), request.URL.Query().Get("through")
		items, err := h.api.ExportInvestmentTrades(request.Context(), userID, from, through)
		if err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		contents, err := investmentTradesCSV(items)
		if err != nil {
			writeError(w, request, h.options.Logger, apperrors.Internal(fmt.Errorf("encode investment CSV: %w", err)))
			return
		}
		writeCSV(w, fmt.Sprintf("money-manager-investments-%s-to-%s.csv", from, through), contents)
	}))
}

func (h *handler) registerInvestmentScheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /investment-schedules", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		items, err := h.api.ListInvestmentSchedules(request.Context(), userID, request.URL.Query().Get("status"))
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, items, err)
	}))
	mux.HandleFunc("POST /investment-schedules", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.InvestmentScheduleRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.CreateInvestmentSchedule(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, item, err)
	}))
	mux.HandleFunc("GET /investment-schedules/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		item, err := h.api.GetInvestmentSchedule(request.Context(), userID, scheduleID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("PUT /investment-schedules/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		var payload model.InvestmentScheduleRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.UpdateInvestmentSchedule(request.Context(), userID, scheduleID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("POST /investment-schedules/{id}/pause", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		item, err := h.api.PauseInvestmentSchedule(request.Context(), userID, scheduleID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("POST /investment-schedules/{id}/resume", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		item, err := h.api.ResumeInvestmentSchedule(request.Context(), userID, scheduleID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("DELETE /investment-schedules/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		if err := h.api.DeleteInvestmentSchedule(request.Context(), userID, scheduleID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
