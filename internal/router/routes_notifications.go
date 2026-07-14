package router

import (
	"net/http"

	"money-manager-server/internal/model"
)

func (h *handler) registerNotificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /notification-preferences", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		item, err := h.api.GetNotificationPreferences(request.Context(), userID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("PUT /notification-preferences", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.NotificationPreferences
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.UpdateNotificationPreferences(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, item, err)
	}))
	mux.HandleFunc("POST /push-devices", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		var payload model.PushDeviceRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		item, err := h.api.RegisterPushDevice(request.Context(), userID, payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, item, err)
	}))
	mux.HandleFunc("DELETE /push-devices/{id}", h.requireUserResource(func(w http.ResponseWriter, request *http.Request, userID, deviceID int) {
		if err := h.api.DeletePushDevice(request.Context(), userID, deviceID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
