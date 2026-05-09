package router

import (
	"encoding/json"
	"money-manager-server/internal/model"
	"money-manager-server/internal/service"
	"net/http"
	"strconv"
	"strings"
)

func Build(s *service.Service) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })
	m.HandleFunc("POST /auth/register", func(w http.ResponseWriter, r *http.Request) {
		var req model.AuthRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp, err := s.Register(r.Context(), req)
		write(w, 201, resp, err)
	})
	m.HandleFunc("POST /auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req model.AuthRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp, err := s.Login(r.Context(), req)
		write(w, 200, resp, err)
	})
	m.HandleFunc("GET /transactions", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := authUser(w, r, s)
		if !ok {
			return
		}
		out, err := s.Repo.ListTransactions(r.Context(), uid, r.URL.Query().Get("month"), r.URL.Query().Get("type"), r.URL.Query().Get("category"))
		write(w, 200, out, err)
	})
	m.HandleFunc("GET /transactions/summary", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := authUser(w, r, s)
		if !ok {
			return
		}
		out, err := s.Repo.Summary(r.Context(), uid, r.URL.Query().Get("month"))
		write(w, 200, out, err)
	})
	m.HandleFunc("POST /transactions", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := authUser(w, r, s)
		if !ok {
			return
		}
		var req model.TransactionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Currency == "" {
			req.Currency = "EUR"
		}
		out, err := s.Repo.CreateTransaction(r.Context(), uid, req)
		write(w, 200, out, err)
	})
	m.HandleFunc("PUT /transactions/{id}", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := authUser(w, r, s)
		if !ok {
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		var req model.TransactionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Currency == "" {
			req.Currency = "EUR"
		}
		out, err := s.Repo.UpdateTransaction(r.Context(), uid, id, req)
		write(w, 200, out, err)
	})
	m.HandleFunc("DELETE /transactions/{id}", func(w http.ResponseWriter, r *http.Request) {
		uid, ok := authUser(w, r, s)
		if !ok {
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		err := s.Repo.DeleteTransaction(r.Context(), uid, id)
		if err != nil {
			write(w, 400, nil, err)
			return
		}
		w.WriteHeader(204)
	})
	return m
}

func authUser(w http.ResponseWriter, r *http.Request, s *service.Service) (int, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		w.WriteHeader(401)
		return 0, false
	}
	uid, err := s.ParseUserID(strings.TrimPrefix(h, "Bearer "))
	if err != nil {
		w.WriteHeader(401)
		return 0, false
	}
	return uid, true
}
func write(w http.ResponseWriter, code int, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
