package router

import (
	"net/http"

	"money-manager-server/internal/apperrors"
)

type handler struct {
	api         API
	options     Options
	authLimiter *authRateLimiter
}

type authenticatedHandler func(http.ResponseWriter, *http.Request, int)
type authenticatedResourceHandler func(http.ResponseWriter, *http.Request, int, int)

func (h *handler) requireUser(next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		userID, ok := authenticatedUser(w, request, h.api, h.options.Logger)
		if !ok {
			return
		}
		next(w, request, userID)
	}
}

func (h *handler) requireUserResource(next authenticatedResourceHandler) http.HandlerFunc {
	return h.requireUser(func(w http.ResponseWriter, request *http.Request, userID int) {
		resourceID, err := parseID(request.PathValue("id"))
		if err != nil {
			writeError(w, request, h.options.Logger, err)
			return
		}
		next(w, request, userID, resourceID)
	})
}

func (h *handler) notFound(w http.ResponseWriter, request *http.Request) {
	writeError(w, request, h.options.Logger, apperrors.NotFound("endpoint not found"))
}
