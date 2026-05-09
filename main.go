package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type API struct {
	mu           sync.RWMutex
	nextTxnID    int
	transactions map[int]Transaction
}

type Transaction struct {
	ID          int       `json:"id"`
	Amount      float64   `json:"amount"`
	Type        string    `json:"type"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Date        time.Time `json:"date"`
}

type createTransactionRequest struct {
	Amount      float64 `json:"amount"`
	Type        string  `json:"type"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Date        string  `json:"date"`
}

func main() {
	port := getenv("PORT", "8080")

	api := &API{nextTxnID: 1, transactions: map[int]Transaction{}}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /auth/register", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusCreated, map[string]any{"message": "registered", "token": "dev-token"})
	})

	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]any{"message": "logged_in", "token": "dev-token"})
	})

	mux.HandleFunc("GET /transactions", api.listTransactions)
	mux.HandleFunc("POST /transactions", api.createTransaction)
	mux.HandleFunc("GET /transactions/summary", api.transactionsSummary)
	mux.HandleFunc("PUT /transactions/{id}", api.updateTransaction)
	mux.HandleFunc("DELETE /transactions/{id}", api.deleteTransaction)

	fmt.Printf("Money Manager API listening on :%s\n", port)
	if err := http.ListenAndServe(":"+port, loggingMiddleware(mux)); err != nil {
		panic(err)
	}
}

func (a *API) listTransactions(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	resp := make([]Transaction, 0, len(a.transactions))
	for _, tx := range a.transactions {
		resp = append(resp, tx)
	}

	respondJSON(w, http.StatusOK, resp)
}

func (a *API) createTransaction(w http.ResponseWriter, r *http.Request) {
	var req createTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	txDate := time.Now().UTC()
	if req.Date != "" {
		parsed, err := time.Parse(time.RFC3339, req.Date)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "date must be RFC3339"})
			return
		}
		txDate = parsed
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	tx := Transaction{
		ID:          a.nextTxnID,
		Amount:      req.Amount,
		Type:        req.Type,
		Category:    req.Category,
		Description: req.Description,
		Date:        txDate,
	}
	a.transactions[tx.ID] = tx
	a.nextTxnID++

	respondJSON(w, http.StatusCreated, tx)
}

func (a *API) updateTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var req createTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	tx, ok := a.transactions[id]
	if !ok {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "transaction not found"})
		return
	}

	tx.Amount = req.Amount
	tx.Type = req.Type
	tx.Category = req.Category
	tx.Description = req.Description
	if req.Date != "" {
		parsed, err := time.Parse(time.RFC3339, req.Date)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "date must be RFC3339"})
			return
		}
		tx.Date = parsed
	}
	a.transactions[id] = tx

	respondJSON(w, http.StatusOK, tx)
}

func (a *API) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.transactions[id]; !ok {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "transaction not found"})
		return
	}
	delete(a.transactions, id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) transactionsSummary(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	income := 0.0
	expense := 0.0
	for _, tx := range a.transactions {
		switch strings.ToLower(tx.Type) {
		case "income":
			income += tx.Amount
		default:
			expense += tx.Amount
		}
	}

	respondJSON(w, http.StatusOK, map[string]float64{
		"income":  income,
		"expense": expense,
		"net":     income - expense,
	})
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%s %s\n", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
