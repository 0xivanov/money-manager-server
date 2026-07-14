package router

import (
	"net/http"

	"money-manager-server/internal/model"
)

func (h *handler) registerHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, http.StatusOK, "ok")
	})
	readiness := func(w http.ResponseWriter, request *http.Request) {
		if err := h.api.Ready(request.Context()); err != nil {
			writeText(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		writeText(w, http.StatusOK, "ok")
	}
	mux.HandleFunc("GET /readyz", readiness)
	mux.HandleFunc("GET /health", readiness)
}

func (h *handler) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/register", func(w http.ResponseWriter, request *http.Request) {
		var payload model.AuthRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		if !allowAuthRequest(w, request, payload.Email, h.authLimiter, h.options) {
			return
		}
		response, err := h.api.Register(request.Context(), payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusCreated, response, err)
	})
	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, request *http.Request) {
		var payload model.AuthRequest
		if err := decodeJSON(w, request, &payload, h.options.RequestBodyLimit); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		if !allowAuthRequest(w, request, payload.Email, h.authLimiter, h.options) {
			return
		}
		response, err := h.api.Login(request.Context(), payload)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, response, err)
	})
}

func (h *handler) registerProfileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /me", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		user, err := h.api.GetMe(request.Context(), userID)
		writeJSONResult(w, request, h.options.Logger, http.StatusOK, user, err)
	}))
	mux.HandleFunc("DELETE /me", h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		if err := h.api.DeleteMe(request.Context(), userID); err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
