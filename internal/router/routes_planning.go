package router

import (
	"net/http"
	"strings"

	"money-manager-server/internal/model"
)

func (h *handler) registerTransactionScheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /schedules", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		items, err := h.api.ListTransactionSchedules(request.Context(), userID, request.URL.Query().Get("status"))
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, items, err)
	}))
	mux.HandleFunc("POST /schedules", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.TransactionScheduleRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.CreateTransactionSchedule(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, item, err)
	}))
	mux.HandleFunc("GET /schedules/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		item, err := h.api.GetTransactionSchedule(request.Context(), userID, scheduleID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("PUT /schedules/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		var payload model.TransactionScheduleRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.UpdateTransactionSchedule(request.Context(), userID, scheduleID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("POST /schedules/{id}/pause", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		item, err := h.api.PauseTransactionSchedule(request.Context(), userID, scheduleID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("POST /schedules/{id}/resume", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		item, err := h.api.ResumeTransactionSchedule(request.Context(), userID, scheduleID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("DELETE /schedules/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, scheduleID int) {
		if err := h.api.DeleteTransactionSchedule(request.Context(), userID, scheduleID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /schedule-occurrences", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		query := request.URL.Query()
		scheduleID := 0
		var err error
		if rawScheduleID := strings.TrimSpace(query.Get("schedule_id")); rawScheduleID != "" {
			scheduleID, err = parseID(rawScheduleID)
		}
		if err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		items, err := h.api.ListTransactionScheduleOccurrences(
			request.Context(), userID, query.Get("from"), query.Get("through"), scheduleID, query.Get("status"),
		)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, items, err)
	}))
}

func (h *handler) registerBudgetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /budgets", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		includeArchived := strings.EqualFold(request.URL.Query().Get("include_archived"), "true")
		items, err := h.api.ListBudgets(request.Context(), userID, includeArchived)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, items, err)
	}))
	mux.HandleFunc("POST /budgets", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.BudgetRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.CreateBudget(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, item, err)
	}))
	mux.HandleFunc("GET /budgets/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, budgetID int) {
		item, err := h.api.GetBudget(request.Context(), userID, budgetID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("PUT /budgets/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, budgetID int) {
		var payload model.BudgetRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.UpdateBudget(request.Context(), userID, budgetID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("DELETE /budgets/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, budgetID int) {
		if err := h.api.DeleteBudget(request.Context(), userID, budgetID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
